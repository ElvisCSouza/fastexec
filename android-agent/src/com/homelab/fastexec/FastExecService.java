package com.homelab.fastexec;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.Service;
import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.net.wifi.WifiManager;
import android.os.Build;
import android.os.Handler;
import android.os.HandlerThread;
import android.os.IBinder;
import android.os.Looper;
import android.os.PowerManager;
import android.provider.Settings;
import android.util.Log;

import java.io.BufferedReader;
import java.io.File;
import java.io.InputStreamReader;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.util.concurrent.TimeUnit;

public class FastExecService extends Service {
    private static final String TAG = "FastExecService";
    private static final String CHANNEL_ID = "fastexec_channel";
    private static final int CMD_PORT = 9143;

    // Auto-TV: o Firestick acordar (Alexa "ligar a TV", WakeLock) nao liga a TV; a tecla Home dispara o
    // HDMI-CEC One Touch Play. Ao acordar por qualquer motivo, se estiver na tela inicial, manda Home.
    // Medido na Suite em 28/out/2026. Liga/desliga pelo comando "autotv on|off" (persistente).
    private static final String PREFS = "fastexec";
    private static final String PREF_AUTO_TV = "auto_tv";
    private static final long AUTO_TV_DELAY_MS = 1500;
    private static final long SELF_WAKE_WINDOW_MS = 5000;

    // Aviso de estado: a cada tela ligou/desligou (e ao iniciar o servico) o app avisa o tv-fastbridge
    // no noteserver com uma linha "ON"/"OFF" (ele identifica o Firestick pelo IP). Sem consulta periodica.
    // Se o aviso falhar, tenta de novo (5 s, 15 s, 60 s...) enquanto o estado nao mudar.
    private static final String PREF_REPORT_TARGET = "report_target";
    private static final String DEFAULT_REPORT_TARGET = "192.168.31.142:9144";
    private static final long[] REPORT_RETRY_MS = {5000, 15000, 60000};

    // CEC no proprio evento de tela (substitui o watcher por poll) + aviso de heal sob demanda.
    // No screen-off/no wake o app roda o CEC via o su do GhostLock. Se o root cair, ele avisa o
    // daemon fastexec-heal (TCP cru, sem HTTP) que repoe o root e manda o CEC. Sem heartbeat.
    private static final String PREF_HEAL_TARGET = "heal_target";
    private static final String DEFAULT_HEAL_TARGET = "192.168.31.142:9145";
    private static final String PREF_TV_DEV = "tv_dev";            // "sala" | "suite" | "" (desligado)
    private static final String PREF_TV_CEC_ROOT = "tv_cec_root";  // liga o CEC-via-su no evento de tela
    private static final String SU_BIN = "/data/local/tmp/su";
    private static final long HEAL_DEBOUNCE_MS = 60000;            // nao avisa o heal mais de 1x/min

    // Comandos CEC (transacoes conferidas no Fire OS 8.1.8.5):
    //   service call hdmi_control 30 i32 0 i32 0  -> powerOffRemoteDevice (<Standby>)
    //   service call AmazonHdmiService 21         -> turnTvOn() (<Text View On>+<Active Source> = OTP)
    private static final String CEC_STANDBY = "service call hdmi_control 30 i32 0 i32 0";
    private static final String CEC_TURNON = "service call AmazonHdmiService 21";

    private volatile boolean isRunning = false;
    private Thread workerThread;
    private Thread cmdThread;
    private ServerSocket cmdServerSocket;
    private volatile long lastSelfWakeMs = 0;
    private final Handler handler = new Handler(Looper.getMainLooper());
    private BroadcastReceiver screenReceiver;
    private HandlerThread reportThread;
    private Handler reportHandler;
    private volatile int reportSeq = 0;
    private volatile long lastHealMs = 0;
    private WifiManager.WifiLock wifiLock;

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }

    @Override
    public void onCreate() {
        super.onCreate();
        createNotificationChannel();
        startForeground(1, createNotification());
        acquireWifiLock();
        reportThread = new HandlerThread("FastExecReport");
        reportThread.start();
        reportHandler = new Handler(reportThread.getLooper());
        startDaemon();
        startCommandServer();
        ensureAccessibilityEnabled();
        registerScreenReceiver();
        ensureAdbOn();
        reportCurrentState();
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        if (!isRunning) {
            startDaemon();
            startCommandServer();
        }
        ensureAdbOn();
        return START_STICKY;
    }

    /** Religa a propria acessibilidade se o Android a desligou (force-stop, atualizacao do Fire OS). Usa a
     *  WRITE_SECURE_SETTINGS concedida por ADB; sem ela, so registra no log. Roda so ao iniciar o servico. */
    private void ensureAccessibilityEnabled() {
        String me = getPackageName() + "/" + FastExecAccessibilityService.class.getName();
        try {
            String cur = Settings.Secure.getString(getContentResolver(), Settings.Secure.ENABLED_ACCESSIBILITY_SERVICES);
            if (cur != null && cur.contains(me)) return;
            String novo = (cur == null || cur.isEmpty()) ? me : cur + ":" + me;
            Settings.Secure.putString(getContentResolver(), Settings.Secure.ENABLED_ACCESSIBILITY_SERVICES, novo);
            Settings.Secure.putInt(getContentResolver(), Settings.Secure.ACCESSIBILITY_ENABLED, 1);
            Log.w(TAG, "acessibilidade estava desligada; religada");
        } catch (SecurityException e) {
            Log.w(TAG, "acessibilidade desligada e sem permissao para religar");
        }
    }

    /** ADB definitivo: se alguem (ou o Fire OS) desligar o ADB, religa sozinho. So age quando esta OFF.
     *  Nunca DESliga (desligar o ADB mata o root temporario). Precisa de WRITE_SECURE_SETTINGS. */
    private void ensureAdbOn() {
        try {
            int v = Settings.Global.getInt(getContentResolver(), Settings.Global.ADB_ENABLED, 0);
            if (v != 1) {
                Settings.Global.putInt(getContentResolver(), Settings.Global.ADB_ENABLED, 1);
                Log.i(TAG, "ensureAdbOn: ADB estava OFF; religado");
            }
        } catch (SecurityException e) {
            Log.w(TAG, "ensureAdbOn: sem WRITE_SECURE_SETTINGS");
        }
    }

    private boolean isAutoTvEnabled() {
        return getSharedPreferences(PREFS, MODE_PRIVATE).getBoolean(PREF_AUTO_TV, true);
    }

    private void setAutoTvEnabled(boolean on) {
        getSharedPreferences(PREFS, MODE_PRIVATE).edit().putBoolean(PREF_AUTO_TV, on).apply();
    }

    private String reportTarget() {
        return getSharedPreferences(PREFS, MODE_PRIVATE).getString(PREF_REPORT_TARGET, DEFAULT_REPORT_TARGET);
    }

    private String healTarget() {
        return getSharedPreferences(PREFS, MODE_PRIVATE).getString(PREF_HEAL_TARGET, DEFAULT_HEAL_TARGET);
    }

    private String tvDev() {
        return getSharedPreferences(PREFS, MODE_PRIVATE).getString(PREF_TV_DEV, "");
    }

    private boolean isTvCecRootEnabled() {
        return getSharedPreferences(PREFS, MODE_PRIVATE).getBoolean(PREF_TV_CEC_ROOT, true);
    }

    // ---- CEC via root no evento de tela + heal sob demanda ---------------------------------------

    /** Roda um comando como root pelo su do GhostLock. Retorna true se o su respondeu (uid=0/exit 0).
     *  NAO roda na main thread. O su do GhostLock e um cliente que fala com um daemon em
     *  /data/local/tmp/temp_su.sock; se o root caiu, ele falha na hora ("Connection refused"). */
    private boolean suExec(String cmd) {
        try {
            ProcessBuilder pb = new ProcessBuilder(SU_BIN, "-c", cmd);
            pb.redirectErrorStream(true);
            Process p = pb.start();
            StringBuilder sb = new StringBuilder();
            BufferedReader r = new BufferedReader(new InputStreamReader(p.getInputStream()));
            String l;
            while ((l = r.readLine()) != null) sb.append(l).append('\n');
            boolean done = p.waitFor(15, TimeUnit.SECONDS);
            if (!done) p.destroyForcibly();
            String outp = sb.toString();
            boolean ok = done && p.exitValue() == 0
                    && !outp.contains("connect daemon")
                    && !outp.contains("Permission denied")
                    && !outp.contains("inaccessible");
            if (!ok) Log.w(TAG, "suExec falhou (" + (done ? "rc=" + p.exitValue() : "timeout") + "): " + outp.trim());
            return ok;
        } catch (Exception e) {
            Log.w(TAG, "suExec erro: " + e.getMessage());
            return false;
        }
    }

    /** Screen-off: manda o <Standby> via su. Sem root -> avisa o daemon de heal (TVOFF) 1x. */
    private void handleScreenOff() {
        new Thread(new Runnable() {
            @Override
            public void run() {
                boolean ok = false;
                if (isTvCecRootEnabled()) ok = suExec(CEC_STANDBY);
                if (ok) Log.i(TAG, "cec: Standby enviado via su");
                else notifyHeal("TVOFF");
            }
        }, "FastExecCecOff").start();
    }

    /** Wake: reforca o One Touch Play via su (o autotv ja manda Home). Sem root -> avisa o heal. */
    private void handleScreenOn() {
        new Thread(new Runnable() {
            @Override
            public void run() {
                if (!isTvCecRootEnabled()) return;
                if (!suExec(CEC_TURNON)) notifyHeal("TVON");
            }
        }, "FastExecCecOn").start();
    }

    /** Avisa o daemon fastexec-heal (TCP cru, sem HTTP) para repor o root. Debounce de 1/min. */
    private void notifyHeal(String cmd) {
        String dev = tvDev();
        if (dev == null || dev.isEmpty()) {
            Log.w(TAG, "heal: tv_dev nao configurado, sem aviso");
            return;
        }
        synchronized (this) {
            long now = System.currentTimeMillis();
            if (now - lastHealMs < HEAL_DEBOUNCE_MS) {
                Log.i(TAG, "heal: debounce, ignorando " + cmd);
                return;
            }
            lastHealMs = now;
        }
        String target = healTarget();
        try {
            int i = target.lastIndexOf(':');
            String host = target.substring(0, i);
            int port = Integer.parseInt(target.substring(i + 1));
            Socket s = new Socket();
            s.connect(new InetSocketAddress(host, port), 2000);
            s.setSoTimeout(2000);
            OutputStream out = s.getOutputStream();
            out.write((cmd + " " + dev + "\n").getBytes());
            out.flush();
            BufferedReader in = new BufferedReader(new InputStreamReader(s.getInputStream()));
            String ack = in.readLine();
            s.close();
            Log.i(TAG, "heal: " + cmd + " " + dev + " -> " + target + " (" + ack + ")");
        } catch (Exception e) {
            Log.w(TAG, "heal: " + cmd + " falhou para " + target + ": " + e.getMessage());
        }
    }

    // ---- aviso de estado -------------------------------------------------------------------------

    private void reportCurrentState() {
        PowerManager pm = (PowerManager) getSystemService(Context.POWER_SERVICE);
        reportState(pm != null && pm.isInteractive());
    }

    /** Agenda o envio do estado; um estado novo cancela as tentativas do anterior. */
    private void reportState(final boolean on) {
        final int seq = ++reportSeq;
        reportHandler.removeCallbacksAndMessages(null);
        reportHandler.post(new Runnable() {
            int attempt = 0;
            @Override
            public void run() {
                if (seq != reportSeq) return;
                if (sendReport(on ? "ON" : "OFF")) return;
                long wait = REPORT_RETRY_MS[Math.min(attempt, REPORT_RETRY_MS.length - 1)];
                attempt++;
                reportHandler.postDelayed(this, wait);
            }
        });
    }

    private boolean sendReport(String state) {
        String target = reportTarget();
        try {
            int i = target.lastIndexOf(':');
            String host = target.substring(0, i);
            int port = Integer.parseInt(target.substring(i + 1));
            Socket s = new Socket();
            s.connect(new InetSocketAddress(host, port), 2000);
            s.setSoTimeout(2000);
            OutputStream out = s.getOutputStream();
            out.write((state + "\n").getBytes());
            out.flush();
            BufferedReader in = new BufferedReader(new InputStreamReader(s.getInputStream()));
            String ack = in.readLine();
            s.close();
            Log.i(TAG, "report: " + state + " -> " + target + " (" + ack + ")");
            return ack != null && ack.startsWith("OK");
        } catch (Exception e) {
            Log.w(TAG, "report: " + state + " falhou para " + target + ": " + e.getMessage());
            return false;
        }
    }

    // ---- tela ligou / desligou -------------------------------------------------------------------

    private void registerScreenReceiver() {
        if (screenReceiver != null) return;
        screenReceiver = new BroadcastReceiver() {
            @Override
            public void onReceive(Context context, Intent intent) {
                String action = intent.getAction();
                if (Intent.ACTION_SCREEN_OFF.equals(action)) {
                    reportState(false);
                    handleScreenOff();
                    return;
                }
                if (!Intent.ACTION_SCREEN_ON.equals(action)) return;
                reportState(true);
                ensureAdbOn();
                handleScreenOn();
                if (!isAutoTvEnabled()) {
                    Log.i(TAG, "autotv: tela ligou, desativado");
                    return;
                }
                if (System.currentTimeMillis() - lastSelfWakeMs < SELF_WAKE_WINDOW_MS) {
                    Log.i(TAG, "autotv: acordado pelo comando wake, quem chamou cuida do Home");
                    return;
                }
                handler.postDelayed(new Runnable() {
                    @Override
                    public void run() {
                        autoTvCheck();
                    }
                }, AUTO_TV_DELAY_MS);
            }
        };
        IntentFilter filter = new IntentFilter(Intent.ACTION_SCREEN_ON);
        filter.addAction(Intent.ACTION_SCREEN_OFF);
        registerReceiver(screenReceiver, filter);
        Log.i(TAG, "receiver de tela registrado (autotv + aviso de estado)");
    }

    private void autoTvCheck() {
        PowerManager pm = (PowerManager) getSystemService(Context.POWER_SERVICE);
        if (pm == null || !pm.isInteractive()) {
            Log.i(TAG, "autotv: voltou a dormir, nada a fazer");
            return;
        }
        FastExecAccessibilityService a11y = FastExecAccessibilityService.getInstance();
        if (a11y == null) {
            Log.w(TAG, "autotv: acessibilidade desligada, sem Home");
            return;
        }
        String app = a11y.getCurrentApp();
        // So na tela inicial (ou sem evento ainda): se o usuario abriu um app pelo controle, nao interromper.
        if ("unknown".equals(app) || app.contains("launcher")) {
            boolean ok = a11y.goHome();
            Log.i(TAG, "autotv: Home enviado (app=" + app + ", ok=" + ok + ")");
        } else {
            Log.i(TAG, "autotv: app em primeiro plano (" + app + "), sem Home");
        }
    }

    private void createNotificationChannel() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            NotificationChannel channel = new NotificationChannel(
                    CHANNEL_ID,
                    "FastExec Service",
                    NotificationManager.IMPORTANCE_MIN
            );
            channel.setDescription("FastExec Core Daemon");
            channel.setShowBadge(false);
            NotificationManager manager = getSystemService(NotificationManager.class);
            if (manager != null) {
                manager.createNotificationChannel(channel);
            }
        }
    }

    private Notification createNotification() {
        Notification.Builder builder;
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            builder = new Notification.Builder(this, CHANNEL_ID);
        } else {
            builder = new Notification.Builder(this);
        }
        return builder
                .setContentTitle("FastExec Core")
                .setContentText("Portas 9142 e 9143 ativas")
                .setSmallIcon(android.R.drawable.ic_menu_manage)
                .setOngoing(true)
                .build();
    }

    private synchronized void startDaemon() {
        if (isRunning) return;
        isRunning = true;

        workerThread = new Thread(new Runnable() {
            @Override
            public void run() {
                File bin = installBinary();
                if (bin == null || !bin.exists()) {
                    Log.e(TAG, "Failed to install server-exec binary");
                    isRunning = false;
                    return;
                }

                while (isRunning) {
                    try {
                        Log.i(TAG, "Starting server-exec: " + bin.getAbsolutePath());
                        ProcessBuilder pb = new ProcessBuilder(bin.getAbsolutePath());
                        pb.redirectErrorStream(true);
                        Process process = pb.start();

                        BufferedReader reader = new BufferedReader(
                                new InputStreamReader(process.getInputStream())
                        );
                        String line;
                        while ((line = reader.readLine()) != null) {
                            Log.i(TAG, "[server-exec] " + line);
                        }

                        int exitCode = process.waitFor();
                        Log.w(TAG, "server-exec exited with code: " + exitCode);
                    } catch (InterruptedException e) {
                        Log.i(TAG, "Worker thread interrupted");
                        break;
                    } catch (Exception e) {
                        Log.e(TAG, "Error executing server-exec", e);
                    }

                    if (isRunning) {
                        try {
                            Thread.sleep(3000);
                        } catch (InterruptedException e) {
                            break;
                        }
                    }
                }
                isRunning = false;
            }
        }, "FastExecDaemonThread");
        workerThread.start();
    }

    private void startCommandServer() {
        cmdThread = new Thread(new Runnable() {
            @Override
            public void run() {
                try {
                    cmdServerSocket = new ServerSocket(CMD_PORT);
                    Log.i(TAG, "Command server listening on port " + CMD_PORT);
                    while (isRunning) {
                        Socket socket = cmdServerSocket.accept();
                        handleCommandClient(socket);
                    }
                } catch (Exception e) {
                    if (isRunning) {
                        Log.e(TAG, "Command server error", e);
                    }
                }
            }
        }, "FastExecCmdThread");
        cmdThread.start();
    }

    private void handleCommandClient(final Socket socket) {
        new Thread(new Runnable() {
            @Override
            public void run() {
                try {
                    BufferedReader in = new BufferedReader(new InputStreamReader(socket.getInputStream()));
                    OutputStream out = socket.getOutputStream();
                    String line = in.readLine();
                    if (line != null) {
                        String rawCmd = line.trim();
                        String cmd = rawCmd.toLowerCase();
                        Log.i(TAG, "Cmd received: " + rawCmd);

                        if ("wake".equals(cmd)) {
                            wakeDevice();
                            out.write("OK:WAKE\n".getBytes());
                        } else if ("sleep".equals(cmd)) {
                            boolean ok = sleepDevice();
                            out.write((ok ? "OK:SLEEP\n" : "FAIL:NO_A11Y\n").getBytes());
                        } else if ("power".equals(cmd)) {
                            PowerManager pm = (PowerManager) getSystemService(Context.POWER_SERVICE);
                            if (pm != null && pm.isInteractive()) {
                                sleepDevice();
                                out.write("OK:SLEEP\n".getBytes());
                            } else {
                                wakeDevice();
                                out.write("OK:WAKE\n".getBytes());
                            }
                        } else if ("state".equals(cmd)) {
                            PowerManager pm = (PowerManager) getSystemService(Context.POWER_SERVICE);
                            out.write((pm != null && pm.isInteractive() ? "ON\n" : "OFF\n").getBytes());
                        } else if ("report".equals(cmd)) {
                            reportCurrentState();
                            out.write("OK:REPORT\n".getBytes());
                        } else if (cmd.startsWith("report_target ")) {
                            String t = rawCmd.substring(14).trim();
                            getSharedPreferences(PREFS, MODE_PRIVATE).edit().putString(PREF_REPORT_TARGET, t).apply();
                            reportCurrentState();
                            out.write(("OK:REPORT_TARGET " + t + "\n").getBytes());
                        } else if (cmd.startsWith("tvdev ")) {
                            String d = rawCmd.substring(6).trim().toLowerCase();
                            if ("off".equals(d) || "none".equals(d)) d = "";
                            getSharedPreferences(PREFS, MODE_PRIVATE).edit().putString(PREF_TV_DEV, d).apply();
                            out.write(("OK:TVDEV " + (d.isEmpty() ? "(off)" : d) + "\n").getBytes());
                        } else if ("tvdev".equals(cmd) || "tvdev status".equals(cmd)) {
                            String d = tvDev();
                            out.write(("TVDEV:" + (d.isEmpty() ? "(off)" : d) + "\n").getBytes());
                        } else if (cmd.startsWith("healtarget ")) {
                            String t = rawCmd.substring(11).trim();
                            getSharedPreferences(PREFS, MODE_PRIVATE).edit().putString(PREF_HEAL_TARGET, t).apply();
                            out.write(("OK:HEALTARGET " + t + "\n").getBytes());
                        } else if (cmd.startsWith("cecroot ")) {
                            boolean on = "on".equals(rawCmd.substring(8).trim().toLowerCase());
                            getSharedPreferences(PREFS, MODE_PRIVATE).edit().putBoolean(PREF_TV_CEC_ROOT, on).apply();
                            out.write((on ? "OK:CECROOT_ON\n" : "OK:CECROOT_OFF\n").getBytes());
                        } else if ("cec".equals(cmd) || "cec status".equals(cmd)) {
                            out.write(("CEC:CECROOT=" + (isTvCecRootEnabled() ? "on" : "off") + " TVDEV=" + (tvDev().isEmpty() ? "(off)" : tvDev()) + "\n").getBytes());
                        } else if (cmd.startsWith("healtest ")) {
                            String c = rawCmd.substring(9).trim();
                            notifyHeal(c);
                            out.write(("OK:HEALTEST " + c + "\n").getBytes());
                        } else if (cmd.startsWith("rootcmd ")) {
                            // diagnostico: roda um comando via o su do GhostLock (sem tocar na TV)
                            String c = rawCmd.substring(8).trim();
                            boolean ok = suExec(c);
                            out.write((ok ? "OK:ROOTCMD\n" : "FAIL:ROOTCMD\n").getBytes());
                        } else if ("adb on".equals(cmd) || "adb off".equals(cmd)) {
                            // ADB so ligado na hora do deploy (porta 5555 fechada no resto do tempo). Precisa de
                            // WRITE_SECURE_SETTINGS concedida uma vez: adb shell pm grant com.homelab.fastexec android.permission.WRITE_SECURE_SETTINGS
                            try {
                                Settings.Global.putInt(getContentResolver(), Settings.Global.ADB_ENABLED, "adb on".equals(cmd) ? 1 : 0);
                                out.write(("adb on".equals(cmd) ? "OK:ADB_ON\n" : "OK:ADB_OFF\n").getBytes());
                            } catch (SecurityException e) {
                                out.write("FAIL:NO_PERMISSION\n".getBytes());
                            }
                        } else if ("adb".equals(cmd) || "adb status".equals(cmd)) {
                            int v = Settings.Global.getInt(getContentResolver(), Settings.Global.ADB_ENABLED, 0);
                            out.write((v == 1 ? "ADB:ON\n" : "ADB:OFF\n").getBytes());
                        } else if ("adbaccept".equals(cmd)) {
                            // Aceita o dialogo "Permitir depuracao USB?" (quando o adb fica unauthorized)
                            // por keyevents sinteticos, sem depender de alguem clicar no controle.
                            new Thread(new Runnable() {
                                @Override
                                public void run() {
                                    try {
                                        Runtime.getRuntime().exec(new String[]{"input", "keyevent", "23"});
                                        Thread.sleep(400);
                                        Runtime.getRuntime().exec(new String[]{"input", "keyevent", "66"});
                                    } catch (Exception e) {
                                        Log.w(TAG, "adbaccept: " + e.getMessage());
                                    }
                                }
                            }, "FastExecAdbAccept").start();
                            out.write("OK:ADBACCEPT\n".getBytes());
                        } else if ("home".equals(cmd)) {
                            FastExecAccessibilityService a11y = FastExecAccessibilityService.getInstance();
                            if (a11y != null) {
                                a11y.goHome();
                                out.write("OK:HOME\n".getBytes());
                            } else {
                                out.write("FAIL:NO_A11Y\n".getBytes());
                            }
                        } else if ("back".equals(cmd)) {
                            FastExecAccessibilityService a11y = FastExecAccessibilityService.getInstance();
                            if (a11y != null) {
                                a11y.goBack();
                                out.write("OK:BACK\n".getBytes());
                            } else {
                                out.write("FAIL:NO_A11Y\n".getBytes());
                            }
                        } else if ("shot".equals(cmd)) {
                            FastExecAccessibilityService a11yShot = FastExecAccessibilityService.getInstance();
                            String shot = a11yShot == null ? "FAIL:NO_A11Y" : a11yShot.takeShot(new File(getFilesDir(), "shot.png"));
                            out.write((shot + "\n").getBytes());
                        } else if ("autotv on".equals(cmd)) {                            setAutoTvEnabled(true);
                            out.write("OK:AUTOTV_ON\n".getBytes());
                        } else if ("autotv off".equals(cmd)) {
                            setAutoTvEnabled(false);
                            out.write("OK:AUTOTV_OFF\n".getBytes());
                        } else if ("autotv".equals(cmd) || "autotv status".equals(cmd)) {
                            out.write((isAutoTvEnabled() ? "AUTOTV:ON\n" : "AUTOTV:OFF\n").getBytes());
                        } else if ("up".equals(cmd) || "dpad_up".equals(cmd)) {
                            sendKeyEvent(19);
                            out.write("OK:UP\n".getBytes());
                        } else if ("down".equals(cmd) || "dpad_down".equals(cmd)) {
                            sendKeyEvent(20);
                            out.write("OK:DOWN\n".getBytes());
                        } else if ("left".equals(cmd) || "dpad_left".equals(cmd)) {
                            sendKeyEvent(21);
                            out.write("OK:LEFT\n".getBytes());
                        } else if ("right".equals(cmd) || "dpad_right".equals(cmd)) {
                            sendKeyEvent(22);
                            out.write("OK:RIGHT\n".getBytes());
                        } else if ("select".equals(cmd) || "center".equals(cmd) || "dpad_center".equals(cmd)) {
                            sendKeyEvent(23);
                            out.write("OK:SELECT\n".getBytes());
                        } else if ("volup".equals(cmd) || "volume_up".equals(cmd)) {
                            sendKeyEvent(24);
                            out.write("OK:VOLUP\n".getBytes());
                        } else if ("voldown".equals(cmd) || "volume_down".equals(cmd)) {
                            sendKeyEvent(25);
                            out.write("OK:VOLDOWN\n".getBytes());
                        } else if (cmd.startsWith("launch ")) {
                            String target = rawCmd.substring(7).trim();
                            boolean ok = launchTarget(target);
                            out.write((ok ? "OK:LAUNCH\n" : "FAIL:LAUNCH\n").getBytes());
                        } else if ("app".equals(cmd)) {
                            FastExecAccessibilityService a11y = FastExecAccessibilityService.getInstance();
                            String app = a11y != null ? a11y.getCurrentApp() : "unknown";
                            out.write((app + "\n").getBytes());
                        } else {
                            out.write("ERR:UNKNOWN_CMD\n".getBytes());
                        }
                        out.flush();
                    }
                    socket.close();
                } catch (Exception e) {
                    Log.w(TAG, "Client handle error", e);
                }
            }
        }).start();
    }

    private void sendKeyEvent(int keyCode) {
        try {
            Runtime.getRuntime().exec(new String[]{"input", "keyevent", String.valueOf(keyCode)});
        } catch (Exception e) {
            Log.e(TAG, "sendKeyEvent error: " + keyCode, e);
        }
    }

    private boolean launchTarget(String target) {
        try {
            if (target.startsWith("http://") || target.startsWith("https://")) {
                android.net.Uri uri = android.net.Uri.parse(target);
                Intent intent = new Intent(Intent.ACTION_VIEW, uri);
                intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
                startActivity(intent);
                Log.i(TAG, "Launched URL: " + target);
                return true;
            } else {
                Intent intent = getPackageManager().getLaunchIntentForPackage(target);
                if (intent != null) {
                    intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
                    startActivity(intent);
                    Log.i(TAG, "Launched package: " + target);
                    return true;
                }
            }
        } catch (Exception e) {
            Log.e(TAG, "launchTarget error: " + target, e);
        }
        return false;
    }

    private void wakeDevice() {
        try {
            lastSelfWakeMs = System.currentTimeMillis();
            PowerManager pm = (PowerManager) getSystemService(Context.POWER_SERVICE);
            if (pm != null) {
                PowerManager.WakeLock wl = pm.newWakeLock(
                        PowerManager.SCREEN_BRIGHT_WAKE_LOCK |
                        PowerManager.ACQUIRE_CAUSES_WAKEUP |
                        PowerManager.ON_AFTER_RELEASE,
                        "FastExec:WakeDevice"
                );
                wl.acquire(3000);
                Log.i(TAG, "WakeLock acquired with ACQUIRE_CAUSES_WAKEUP");
            }
        } catch (Exception e) {
            Log.e(TAG, "Failed to wake device", e);
        }
    }

    private boolean sleepDevice() {
        FastExecAccessibilityService a11y = FastExecAccessibilityService.getInstance();
        if (a11y != null) {
            boolean ok = a11y.lockScreen();
            Log.i(TAG, "sleepDevice lockScreen result: " + ok);
            return ok;
        }
        Log.w(TAG, "sleepDevice called but FastExecAccessibilityService is null");
        return false;
    }

    private File installBinary() {
        try {
            File nativeLib = new File(getApplicationInfo().nativeLibraryDir, "libserver-exec.so");
            if (nativeLib.exists() && nativeLib.canExecute()) {
                Log.i(TAG, "Found native library: " + nativeLib.getAbsolutePath());
                return nativeLib;
            }
        } catch (Exception e) {
            Log.w(TAG, "Error checking nativeLibraryDir", e);
        }

        File fallback = new File("/data/local/tmp/server-exec");
        if (fallback.exists()) {
            fallback.setExecutable(true, false);
            return fallback;
        }

        return null;
    }

    /** Mantem o radio Wi-Fi sem power save, inclusive em standby: sem isso o ponto de acesso segura os
     *  pacotes ate o proximo beacon (~100 ms). Sem Wi-Fi conectado (cabo) o lock nao tem efeito. */
    @SuppressWarnings("deprecation")
    private void acquireWifiLock() {
        try {
            WifiManager wm = (WifiManager) getApplicationContext().getSystemService(Context.WIFI_SERVICE);
            wifiLock = wm.createWifiLock(WifiManager.WIFI_MODE_FULL_HIGH_PERF, "fastexec:wifi");
            wifiLock.setReferenceCounted(false);
            wifiLock.acquire();
            Log.i(TAG, "WifiLock high perf adquirido");
        } catch (Exception e) {
            Log.w(TAG, "WifiLock falhou: " + e);
        }
    }
    @Override
    public void onDestroy() {
        isRunning = false;
        if (wifiLock != null && wifiLock.isHeld()) {
            try { wifiLock.release(); } catch (Exception ignored) {}
        }
        if (screenReceiver != null) {
            try { unregisterReceiver(screenReceiver); } catch (Exception ignored) {}
            screenReceiver = null;
        }
        if (reportThread != null) {
            reportThread.quitSafely();
        }
        if (cmdServerSocket != null) {
            try { cmdServerSocket.close(); } catch (Exception ignored) {}
        }
        if (workerThread != null) {
            workerThread.interrupt();
        }
        super.onDestroy();
    }
}
