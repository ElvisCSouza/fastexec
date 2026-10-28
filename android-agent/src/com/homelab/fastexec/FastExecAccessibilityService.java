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

    public String getCurrentApp() {
        return currentApp;
    }
}
