package com.homelab.fastexec;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.Service;
import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.content.SharedPreferences;
import android.os.Build;
import android.os.Handler;
import android.os.IBinder;
import android.os.Looper;
import android.os.PowerManager;
import android.util.Log;

import java.io.BufferedReader;
import java.io.File;
import java.io.InputStreamReader;
import java.io.OutputStream;
import java.net.ServerSocket;
import java.net.Socket;

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

    private volatile boolean isRunning = false;
    private Thread workerThread;
    private Thread cmdThread;
    private ServerSocket cmdServerSocket;
    private volatile long lastSelfWakeMs = 0;
    private final Handler handler = new Handler(Looper.getMainLooper());
    private BroadcastReceiver screenReceiver;

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }

    @Override
    public void onCreate() {
        super.onCreate();
        createNotificationChannel();
        startForeground(1, createNotification());
        startDaemon();
        startCommandServer();
        registerScreenReceiver();
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        if (!isRunning) {
            startDaemon();
            startCommandServer();
        }
        return START_STICKY;
    }

    private boolean isAutoTvEnabled() {
        return getSharedPreferences(PREFS, MODE_PRIVATE).getBoolean(PREF_AUTO_TV, true);
    }

    private void setAutoTvEnabled(boolean on) {
        getSharedPreferences(PREFS, MODE_PRIVATE).edit().putBoolean(PREF_AUTO_TV, on).apply();
    }

    private void registerScreenReceiver() {
        if (screenReceiver != null) return;
        screenReceiver = new BroadcastReceiver() {
            @Override
            public void onReceive(Context context, Intent intent) {
                if (!Intent.ACTION_SCREEN_ON.equals(intent.getAction())) return;
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
        registerReceiver(screenReceiver, new IntentFilter(Intent.ACTION_SCREEN_ON));
        Log.i(TAG, "autotv: receiver de tela registrado");
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
                        } else if ("autotv on".equals(cmd)) {
                            setAutoTvEnabled(true);
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

    @Override
    public void onDestroy() {
        isRunning = false;
        if (screenReceiver != null) {
            try { unregisterReceiver(screenReceiver); } catch (Exception ignored) {}
            screenReceiver = null;
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
