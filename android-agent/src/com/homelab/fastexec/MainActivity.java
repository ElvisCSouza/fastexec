package com.homelab.fastexec;

import android.app.Activity;
import android.content.Intent;
import android.os.Build;
import android.os.Bundle;
import android.widget.TextView;

public class MainActivity extends Activity {
    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);

        Intent serviceIntent = new Intent(this, FastExecService.class);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            startForegroundService(serviceIntent);
        } else {
            startService(serviceIntent);
        }

        TextView tv = new TextView(this);
        tv.setTextSize(22);
        tv.setPadding(40, 40, 40, 40);
        tv.setText("FastExec Core Agent\nStatus: Servico iniciado na porta 9142\nPersistente no Boot");
        setContentView(tv);
    }
}
