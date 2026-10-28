package com.homelab.fastexec;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.os.Build;
import android.util.Log;

public class BootReceiver extends BroadcastReceiver {
    private static final String TAG = "FastExecBoot";

    @Override
    public void onReceive(Context context, Intent intent) {
        Log.i(TAG, "Boot event received: " + intent.getAction());
        Intent serviceIntent = new Intent(context, FastExecService.class);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            context.startForegroundService(serviceIntent);
        } else {
            context.startService(serviceIntent);
        }
    }
}
