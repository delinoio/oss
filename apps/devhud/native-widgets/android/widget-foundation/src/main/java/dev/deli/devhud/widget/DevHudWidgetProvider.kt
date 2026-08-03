package dev.deli.devhud.widget

import android.app.PendingIntent
import android.appwidget.AppWidgetManager
import android.appwidget.AppWidgetProvider
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.view.View
import android.widget.RemoteViews
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch

class DevHudWidgetProvider : AppWidgetProvider() {
    override fun onUpdate(context: Context, manager: AppWidgetManager, appWidgetIds: IntArray) {
        updateWidgets(context, manager, appWidgetIds)
    }

    override fun onAppWidgetOptionsChanged(
        context: Context,
        manager: AppWidgetManager,
        appWidgetId: Int,
        newOptions: Bundle,
    ) {
        updateWidgets(context, manager, intArrayOf(appWidgetId), newOptions)
    }

    private fun updateWidgets(
        context: Context,
        manager: AppWidgetManager,
        appWidgetIds: IntArray,
        options: Bundle? = null,
    ) {
        val result = goAsync()
        CoroutineScope(SupervisorJob() + Dispatchers.IO).launch {
            try {
                val widgets = AndroidWidgetSharedDataAdapter.live(context).readRecord().configuration.widgets
                appWidgetIds.forEach { id ->
                    manager.updateAppWidget(
                        id,
                        views(context, id, widgets, options ?: manager.getAppWidgetOptions(id)),
                    )
                }
            } finally { result.finish() }
        }
    }

    override fun onDeleted(context: Context, appWidgetIds: IntArray) {
        appWidgetIds.forEach { DeckWidgetSelections.remove(context, it) }
    }

    private fun views(
        context: Context,
        appWidgetId: Int,
        widgets: List<DeckWidgetInstance>,
        options: Bundle,
    ): RemoteViews {
        val family = androidWidgetFamily(options)
        val storedId = DeckWidgetSelections.get(context, appWidgetId)
        val selected = widgets.firstOrNull { it.widgetId == storedId && it.family == family }
            ?: widgets.filter { it.family == family }.singleOrNull()
        if (selected != null && storedId == null) {
            DeckWidgetSelections.set(context, appWidgetId, selected.widgetId)
        }
        return RemoteViews(context.packageName, R.layout.devhud_widget).apply {
            if (selected == null) {
                setTextViewText(R.id.devhud_widget_status, context.getString(R.string.devhud_widget_empty))
                setTextViewText(R.id.devhud_widget_count, "—")
                pullRequestRows().forEach { setViewVisibility(it.container, View.GONE) }
                setViewVisibility(R.id.devhud_widget_refresh, View.GONE)
                return@apply
            }
            val snapshot = selected.snapshot
            setTextViewText(R.id.devhud_widget_count, snapshot.matchingCount.toString())
            setTextViewText(R.id.devhud_widget_status, status(context, snapshot))
            setOnClickPendingIntent(
                R.id.devhud_widget_root,
                action(context, appWidgetId, DeckWidgetAction.OpenView(selected.viewId)),
            )
            setOnClickPendingIntent(
                R.id.devhud_widget_refresh,
                action(context, appWidgetId + 10_000, DeckWidgetAction.Refresh(selected.viewId)),
            )
            setViewVisibility(R.id.devhud_widget_refresh, View.VISIBLE)
            val detailLimit = when {
                selected.privacy != DeckWidgetPrivacy.REPOSITORY_AND_TITLES -> 0
                family == DeckWidgetFamily.ANDROID_LIST -> 3
                family == DeckWidgetFamily.ANDROID_WIDE -> 1
                else -> 0
            }
            val pullRequests = snapshot.pullRequests.take(detailLimit)
            pullRequestRows().forEachIndexed { index, row ->
                val pullRequest = pullRequests.getOrNull(index)
                setViewVisibility(row.container, if (pullRequest == null) View.GONE else View.VISIBLE)
                if (pullRequest == null) return@forEachIndexed
                setTextViewText(
                    row.repository,
                    "${pullRequest.repositoryOwner}/${pullRequest.repositoryName} #${pullRequest.number}",
                )
                setTextViewText(row.title, pullRequest.title)
                setContentDescription(
                    row.container,
                    "Open pull request ${pullRequest.number}, ${pullRequest.title}",
                )
                setOnClickPendingIntent(row.container, action(
                    context,
                    appWidgetId + 20_000 + index,
                    DeckWidgetAction.OpenPullRequest(
                        selected.viewId,
                        pullRequest.repositoryOwner,
                        pullRequest.repositoryName,
                        pullRequest.number,
                    ),
                ))
            }
        }
    }

    private data class PullRequestRow(val container: Int, val repository: Int, val title: Int)

    private fun pullRequestRows() = listOf(
        PullRequestRow(R.id.devhud_widget_pull_request_one, R.id.devhud_widget_repository_one, R.id.devhud_widget_title_one),
        PullRequestRow(R.id.devhud_widget_pull_request_two, R.id.devhud_widget_repository_two, R.id.devhud_widget_title_two),
        PullRequestRow(R.id.devhud_widget_pull_request_three, R.id.devhud_widget_repository_three, R.id.devhud_widget_title_three),
    )

    private fun action(context: Context, requestCode: Int, action: DeckWidgetAction): PendingIntent =
        PendingIntent.getActivity(
            context,
            requestCode,
            Intent(Intent.ACTION_VIEW, Uri.parse(action.toAppLink()))
                .setPackage(context.packageName)
                .addFlags(Intent.FLAG_ACTIVITY_CLEAR_TOP or Intent.FLAG_ACTIVITY_SINGLE_TOP),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE,
        )

    private fun status(context: Context, snapshot: WidgetSnapshot): String = when {
        snapshot.offline -> context.getString(R.string.devhud_widget_offline)
        snapshot.freshness == DeckWidgetFreshness.FRESH -> context.getString(R.string.devhud_widget_updated)
        snapshot.freshness == DeckWidgetFreshness.STALE -> context.getString(R.string.devhud_widget_stale)
        snapshot.freshness == DeckWidgetFreshness.DISCONNECTED -> context.getString(R.string.devhud_widget_disconnected)
        else -> context.getString(R.string.devhud_widget_not_refreshed)
    }
}

internal object DeckWidgetSelections {
    private fun preferences(context: Context) = context.getSharedPreferences(
        "devhud-widget-instance-selection.v1",
        Context.MODE_PRIVATE,
    )

    fun get(context: Context, appWidgetId: Int): String? =
        preferences(context).getString(appWidgetId.toString(), null)

    fun set(context: Context, appWidgetId: Int, widgetId: String) {
        preferences(context).edit().putString(appWidgetId.toString(), widgetId).apply()
    }

    fun remove(context: Context, appWidgetId: Int) {
        preferences(context).edit().remove(appWidgetId.toString()).apply()
    }
}

internal fun androidWidgetFamily(options: Bundle): DeckWidgetFamily {
    val width = options.getInt(AppWidgetManager.OPTION_APPWIDGET_MIN_WIDTH)
    val height = options.getInt(AppWidgetManager.OPTION_APPWIDGET_MIN_HEIGHT)
    return when {
        height >= 220 -> DeckWidgetFamily.ANDROID_LIST
        width >= 220 -> DeckWidgetFamily.ANDROID_WIDE
        else -> DeckWidgetFamily.ANDROID_COMPACT
    }
}
