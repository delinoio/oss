package dev.deli.devhud.widget

import android.appwidget.AppWidgetManager
import android.appwidget.AppWidgetProvider
import android.content.Context
import android.widget.RemoteViews
import dev.deli.devhud.widget.foundation.R
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch

/**
 * Build-only provider foundation. The release application never depends on
 * this module and does not register this class as a broadcast receiver.
 */
class DevHudWidgetProvider : AppWidgetProvider() {
    override fun onUpdate(
        context: Context,
        appWidgetManager: AppWidgetManager,
        appWidgetIds: IntArray,
    ) {
        val pendingResult = goAsync()
        CoroutineScope(SupervisorJob() + Dispatchers.IO).launch {
            try {
                val configuration =
                    AndroidWidgetSharedDataAdapter.live(context).readRecord()
                for (appWidgetId in appWidgetIds) {
                    val views =
                        RemoteViews(context.packageName, R.layout.devhud_widget)
                    views.setTextViewText(
                        R.id.devhud_widget_status,
                        if (configuration.configuration.slots.isEmpty()) {
                            context.getString(R.string.devhud_widget_empty)
                        } else {
                            context.getString(R.string.devhud_widget_configured)
                        },
                    )
                    appWidgetManager.updateAppWidget(appWidgetId, views)
                }
            } finally {
                pendingResult.finish()
            }
        }
    }
}
