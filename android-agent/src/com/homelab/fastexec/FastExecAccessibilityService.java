package com.homelab.fastexec;

import android.accessibilityservice.AccessibilityService;
import android.util.Log;
import android.view.accessibility.AccessibilityEvent;

public class FastExecAccessibilityService extends AccessibilityService {
    private static final String TAG = "FastExecA11y";
    private static volatile FastExecAccessibilityService instance;
    private volatile String currentApp = "unknown";

    public static FastExecAccessibilityService getInstance() {
        return instance;
    }

    @Override
    public void onServiceConnected() {
        super.onServiceConnected();
        instance = this;
        Log.i(TAG, "Accessibility service connected");
    }

    @Override
    public void onAccessibilityEvent(AccessibilityEvent event) {
        if (event.getEventType() == AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED) {
            CharSequence pkg = event.getPackageName();
            if (pkg != null) {
                currentApp = pkg.toString();
                Log.d(TAG, "Active package: " + currentApp);
            }
        }
    }

    @Override
    public void onInterrupt() {
        Log.w(TAG, "Accessibility service interrupted");
    }

    @Override
    public void onDestroy() {
        instance = null;
        super.onDestroy();
    }

    public boolean lockScreen() {
        Log.i(TAG, "Executing GLOBAL_ACTION_LOCK_SCREEN");
        return performGlobalAction(GLOBAL_ACTION_LOCK_SCREEN);
    }

    public boolean goHome() {
        return performGlobalAction(GLOBAL_ACTION_HOME);
    }

    public boolean goBack() {
        return performGlobalAction(GLOBAL_ACTION_BACK);
    }

    /** Print da tela (API 30) em PNG no arquivo dado; bloqueia ate 8 s. Retorna "OK:<caminho>" ou "FAIL:<motivo>".
     *  Nao pode rodar na main thread. Janela protegida (DRM) volta falha. */
    public String takeShot(final java.io.File out) {
        if (android.os.Build.VERSION.SDK_INT < 30) return "FAIL:API";
        final java.util.concurrent.CountDownLatch done = new java.util.concurrent.CountDownLatch(1);
        final String[] res = {"FAIL:TIMEOUT"};
        takeScreenshot(android.view.Display.DEFAULT_DISPLAY, getMainExecutor(),
            new AccessibilityService.TakeScreenshotCallback() {
                @Override
                public void onSuccess(AccessibilityService.ScreenshotResult r) {
                    try {
                        android.graphics.Bitmap hw = android.graphics.Bitmap.wrapHardwareBuffer(r.getHardwareBuffer(), r.getColorSpace());
                        android.graphics.Bitmap sw = hw.copy(android.graphics.Bitmap.Config.ARGB_8888, false);
                        java.io.FileOutputStream fo = new java.io.FileOutputStream(out);
                        try {
                            sw.compress(android.graphics.Bitmap.CompressFormat.PNG, 100, fo);
                        } finally {
                            fo.close();
                        }
                        r.getHardwareBuffer().close();
                        res[0] = "OK:" + out.getAbsolutePath();
                    } catch (Exception e) {
                        res[0] = "FAIL:" + e;
                    }
                    done.countDown();
                }

                @Override
                public void onFailure(int errorCode) {
                    res[0] = "FAIL:ERR" + errorCode;
                    done.countDown();
                }
            });
        try { done.await(8, java.util.concurrent.TimeUnit.SECONDS); } catch (InterruptedException ignored) {}
        return res[0];
    }
    public String getCurrentApp() {
        return currentApp;
    }
}
