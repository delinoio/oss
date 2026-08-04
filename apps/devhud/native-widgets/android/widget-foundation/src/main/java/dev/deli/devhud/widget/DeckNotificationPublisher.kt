package dev.deli.devhud.widget

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.core.app.NotificationCompat

object DeckNotificationPublisher {
    private const val CHANNEL_ID = "deck-view-updates"

    fun ensureChannel(context: Context) {
        val manager = context.getSystemService(NotificationManager::class.java)
        val channel = NotificationChannel(
            CHANNEL_ID,
            "Deck view updates",
            NotificationManager.IMPORTANCE_DEFAULT,
        ).apply {
            description = "Updates explicitly enabled for Deck views"
            setBypassDnd(false)
            enableVibration(true)
        }
        manager.createNotificationChannel(channel)
    }

    /** Payload contains only the opaque event ID; optional detail is device-local resolved state. */
    fun publish(
        context: Context,
        payload: Map<String, String>,
        detailedText: String? = null,
        localDetailEnabled: Boolean = false,
    ): Boolean {
        val eventId = DeckNotificationPolicy.eventId(payload) ?: return false
        ensureChannel(context)
        val intent = Intent(
            Intent.ACTION_VIEW,
            Uri.parse(DeckWidgetAction.ResolveEvent(eventId).toAppLink()),
        ).setPackage(context.packageName)
        val pendingIntent = PendingIntent.getActivity(
            context,
            eventId.hashCode(),
            intent,
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )
        val notification = NotificationCompat.Builder(context, CHANNEL_ID)
            .setSmallIcon(android.R.drawable.stat_notify_sync)
            .setContentTitle("Deck")
            .setContentText(DeckNotificationPolicy.text(detailedText, localDetailEnabled))
            .setContentIntent(pendingIntent)
            .setAutoCancel(true)
            .setCategory(NotificationCompat.CATEGORY_STATUS)
            .setPriority(NotificationCompat.PRIORITY_DEFAULT)
            .build()
        context.getSystemService(NotificationManager::class.java)
            .notify(eventId.hashCode(), notification)
        return true
    }
}
