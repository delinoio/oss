package io.delino.devhud.widget

import android.app.Activity
import android.app.PendingIntent
import android.app.job.JobInfo
import android.app.job.JobParameters
import android.app.job.JobScheduler
import android.app.job.JobService
import android.appwidget.AppWidgetManager
import android.appwidget.AppWidgetProvider
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.graphics.Color
import android.net.Uri
import android.os.Bundle
import android.view.Gravity
import android.widget.Button
import android.widget.LinearLayout
import android.widget.RemoteViews
import android.widget.ScrollView
import android.widget.TextView
import io.delino.devhud.R
import org.json.JSONArray
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL
import java.time.Instant
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean

private const val resultLimit = 100
private const val staleAfterMillis = 60 * 60 * 1000L
private const val widgetRefreshJobId = 0x444857
private val widgetExecutor = Executors.newSingleThreadExecutor()

class DevHudWidgetProvider : AppWidgetProvider() {
    override fun onUpdate(context: Context, manager: AppWidgetManager, appWidgetIds: IntArray) {
        appWidgetIds.forEach { renderStored(context, manager, it) }
        scheduleRefresh(context)
    }

    override fun onDeleted(context: Context, appWidgetIds: IntArray) {
        val store = DevHudWidgetStore(context)
        appWidgetIds.forEach(store::removeSelection)
    }

    companion object {
        fun scheduleRefresh(context: Context) {
            val scheduler = context.getSystemService(JobScheduler::class.java)
            scheduler.schedule(JobInfo.Builder(widgetRefreshJobId, ComponentName(context, DevHudWidgetRefreshService::class.java))
                .setRequiredNetworkType(JobInfo.NETWORK_TYPE_ANY)
                .build())
        }

        fun renderStored(context: Context, manager: AppWidgetManager, appWidgetId: Int) {
            val store = DevHudWidgetStore(context)
            val deckId = store.selectedDeckId(appWidgetId)
            val configuration = deckId?.let(store::configuration)
            val snapshot = deckId?.let(store::snapshot)
            manager.updateAppWidget(appWidgetId, render(context, configuration, snapshot, snapshot?.optString("state", "missing-token") ?: "missing-token"))
        }

        fun refresh(context: Context, manager: AppWidgetManager, deckId: String?, appWidgetIds: List<Int>): Boolean {
            val store = DevHudWidgetStore(context)
            if (deckId == null) {
                appWidgetIds.forEach { renderStored(context, manager, it) }
                return true
            }
            val configuration = store.configuration(deckId)
            if (configuration == null) {
                appWidgetIds.forEach { renderStored(context, manager, it) }
                return true
            }
            val previous = store.snapshot(deckId)
            val token = store.token(deckId)
            if (token == null) {
                val snapshot = failure(configuration, previous, "missing-token", Instant.now().toString(), null)
                val stored = store.replaceSnapshot(snapshot)
                renderSelected(context, manager, store, deckId, configuration, snapshot, "missing-token", appWidgetIds)
                return stored
            }
            val snapshot = refreshGitHub(configuration, token, previous)
            val current = store.configuration(deckId)
            if (current == null || !sameSelection(configuration, current)) {
                appWidgetIds.forEach { renderStored(context, manager, it) }
                return true
            }
            val stored = store.replaceSnapshot(snapshot)
            val rendered = if (stored) snapshot else store.snapshot(deckId)
            renderSelected(context, manager, store, deckId, current, rendered, rendered?.optString("state", "error") ?: "error", appWidgetIds)
            return stored
        }

        private fun renderSelected(context: Context, manager: AppWidgetManager, store: DevHudWidgetStore, deckId: String, configuration: JSONObject, snapshot: JSONObject?, forcedState: String, appWidgetIds: List<Int>) {
            appWidgetIds.forEach { appWidgetId ->
                if (store.selectedDeckId(appWidgetId) == deckId) manager.updateAppWidget(appWidgetId, render(context, configuration, snapshot, forcedState))
                else renderStored(context, manager, appWidgetId)
            }
        }

        private fun refreshGitHub(configuration: JSONObject, token: String, previous: JSONObject?): JSONObject {
            val attemptedAt = Instant.now().toString()
            return try {
                val connection = URL("https://api.github.com/search/issues?q=" + Uri.encode(configuration.getString("query")) + "&per_page=100&page=1")
                    .openConnection() as HttpURLConnection
                connection.connectTimeout = 15_000
                connection.readTimeout = 20_000
                connection.setRequestProperty("Accept", "application/vnd.github+json")
                connection.setRequestProperty("Authorization", "Bearer $token")
                connection.setRequestProperty("X-GitHub-Api-Version", "2026-03-10")
                val status = connection.responseCode
                val rateLimited = status == 429 || status == 403 && (connection.getHeaderField("X-RateLimit-Remaining") == "0" || connection.getHeaderField("Retry-After") != null)
                if (rateLimited) return failure(configuration, previous, "rate-limit", attemptedAt, connection)
                if (status == 401 || status == 403) return failure(configuration, previous, "missing-token", attemptedAt, connection)
                if (status !in 200..299) return failure(configuration, previous, "error", attemptedAt, connection)
                val payload = connection.inputStream.bufferedReader().use { JSONObject(it.readText()) }
                if (payload.optBoolean("incomplete_results", false)) return failure(configuration, previous, "error", attemptedAt, connection)
                val items = payload.getJSONArray("items")
                val results = JSONArray()
                var open = 0; var draft = 0; var merged = 0; var closed = 0
                for (index in 0 until minOf(items.length(), resultLimit)) {
                    val item = items.getJSONObject(index)
                    val isDraft = item.optBoolean("draft", false)
                    val isMerged = !item.optJSONObject("pull_request")?.optString("merged_at").isNullOrEmpty()
                    when { isDraft -> draft += 1; isMerged -> merged += 1; item.optString("state") == "closed" -> closed += 1; else -> open += 1 }
                    results.put(JSONObject()
                        .put("nodeId", item.optString("node_id"))
                        .put("number", item.getInt("number"))
                        .put("title", item.getString("title"))
                        .put("repository", repositoryName(item.getString("repository_url")))
                        .put("state", if (isMerged) "merged" else item.optString("state", "open"))
                        .put("draft", isDraft))
                }
                JSONObject()
                    .put("version", 1).put("deckId", configuration.getString("deckId")).put("query", configuration.getString("query"))
                    .put("counts", JSONObject().put("total", payload.getInt("total_count")).put("open", open).put("draft", draft).put("merged", merged).put("closed", closed).put("bounded", payload.getInt("total_count") > resultLimit))
                    .put("results", results).put("state", "fresh").put("lastSuccessfulAt", attemptedAt).put("lastAttemptedAt", attemptedAt)
                    .put("rate", rate(connection))
            } catch (_: Exception) { failure(configuration, previous, "error", attemptedAt, null) }
        }

        private fun failure(configuration: JSONObject, previous: JSONObject?, state: String, attemptedAt: String, connection: HttpURLConnection?): JSONObject {
            val retained = previous?.takeIf { it.optString("query") == configuration.getString("query") } ?: JSONObject().put("version", 1).put("deckId", configuration.getString("deckId")).put("query", configuration.getString("query"))
                .put("counts", JSONObject().put("total", 0).put("open", 0).put("draft", 0).put("merged", 0).put("closed", 0).put("bounded", false))
                .put("results", JSONArray()).put("lastSuccessfulAt", JSONObject.NULL)
            retained.put("state", state).put("lastAttemptedAt", attemptedAt)
            if (connection != null) retained.put("rate", rate(connection))
            return retained
        }

        private fun rate(connection: HttpURLConnection) = JSONObject()
            .put("limit", connection.getHeaderField("X-RateLimit-Limit")?.toIntOrNull() ?: JSONObject.NULL)
            .put("remaining", connection.getHeaderField("X-RateLimit-Remaining")?.toIntOrNull() ?: JSONObject.NULL)
            .put("used", connection.getHeaderField("X-RateLimit-Used")?.toIntOrNull() ?: JSONObject.NULL)
            .put("resetAt", connection.getHeaderField("X-RateLimit-Reset")?.toLongOrNull()?.let { Instant.ofEpochSecond(it).toString() } ?: JSONObject.NULL)
            .put("resource", connection.getHeaderField("X-RateLimit-Resource") ?: JSONObject.NULL)
            .put("retryAfterSeconds", connection.getHeaderField("Retry-After")?.toIntOrNull() ?: JSONObject.NULL)

        private fun repositoryName(url: String): String = url.substringAfter("/repos/", url)

        private fun sameSelection(left: JSONObject, right: JSONObject): Boolean =
            listOf("deckId", "query", "profileId", "profileKind", "scopeId").all { left.optString(it) == right.optString(it) }

        private fun render(context: Context, configuration: JSONObject?, snapshot: JSONObject?, forcedState: String): RemoteViews {
            val views = RemoteViews(context.packageName, R.layout.devhud_widget)
            val deckName = configuration?.optString("name")?.takeIf(String::isNotBlank) ?: context.getString(R.string.devhud_widget_setup)
            views.setTextViewText(R.id.widget_deck_name, deckName)
            val counts = snapshot?.optJSONObject("counts")
            val countsText = if (counts == null) context.getString(R.string.devhud_widget_no_results) else context.getString(R.string.devhud_widget_counts,
                counts.optInt("total"), counts.optInt("open"), counts.optInt("draft"), counts.optInt("merged"), counts.optInt("closed")) + if (counts.optBoolean("bounded")) context.getString(R.string.devhud_widget_first_hundred) else ""
            views.setTextViewText(R.id.widget_counts, countsText)
            val results = snapshot?.optJSONArray("results")
            val previews = (0 until minOf(results?.length() ?: 0, 3)).joinToString("\n") { index ->
                val item = results!!.getJSONObject(index)
                "${item.optString("repository")}#${item.optInt("number")} · ${item.optString("title")}"
            }
            views.setTextViewText(R.id.widget_results, previews.ifEmpty { context.getString(R.string.devhud_widget_no_results) })
            val lastSuccess = snapshot?.optString("lastSuccessfulAt")?.takeIf { it.isNotBlank() && it != "null" }
            val stale = lastSuccess != null && System.currentTimeMillis() - runCatching { Instant.parse(lastSuccess).toEpochMilli() }.getOrDefault(0) >= staleAfterMillis
            val state = if (forcedState == "fresh" && stale) "stale" else forcedState
            val stateLabel = when (state) {
                "stale" -> R.string.devhud_widget_stale
                "missing-token" -> R.string.devhud_widget_missing_token
                "rate-limit" -> R.string.devhud_widget_rate_limit
                "error" -> R.string.devhud_widget_error
                else -> R.string.devhud_widget_fresh
            }
            val stateText = context.getString(stateLabel) + if (stale && state != "stale") " · ${context.getString(R.string.devhud_widget_stale)}" else ""
            views.setTextViewText(R.id.widget_status, context.getString(R.string.devhud_widget_status, stateText, lastSuccess ?: context.getString(R.string.devhud_widget_never)))
            val description = "$deckName. ${listOf(countsText, previews.ifEmpty { null }, stateText, lastSuccess).filterNotNull().joinToString(". ")}"
            views.setContentDescription(R.id.widget_root, description)
            if (configuration != null) {
                val deckId = configuration.getString("deckId")
                val intent = Intent(Intent.ACTION_VIEW, Uri.parse("devhud://deck/$deckId")).setPackage(context.packageName)
                views.setOnClickPendingIntent(R.id.widget_root, PendingIntent.getActivity(context, deckId.hashCode(), intent, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT))
            }
            return views
        }

    }
}

class DevHudWidgetRefreshService : JobService() {
    private val stopped = AtomicBoolean(false)

    override fun onStartJob(parameters: JobParameters): Boolean {
        stopped.set(false)
        widgetExecutor.execute {
            var retry = false
            try {
                val manager = AppWidgetManager.getInstance(applicationContext)
                val component = ComponentName(applicationContext, DevHudWidgetProvider::class.java)
                val store = DevHudWidgetStore(applicationContext)
                for ((deckId, appWidgetIds) in manager.getAppWidgetIds(component).groupBy { store.selectedDeckId(it) }) {
                    if (stopped.get()) { retry = true; break }
                    if (!DevHudWidgetProvider.refresh(applicationContext, manager, deckId, appWidgetIds)) retry = true
                }
            } catch (_: Exception) { retry = true }
            if (!stopped.get()) jobFinished(parameters, retry)
        }
        return true
    }

    override fun onStopJob(parameters: JobParameters): Boolean {
        stopped.set(true)
        return true
    }
}

class DevHudWidgetConfigureActivity : Activity() {
    private var appWidgetId = AppWidgetManager.INVALID_APPWIDGET_ID

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setResult(RESULT_CANCELED)
        appWidgetId = intent?.extras?.getInt(AppWidgetManager.EXTRA_APPWIDGET_ID, AppWidgetManager.INVALID_APPWIDGET_ID) ?: AppWidgetManager.INVALID_APPWIDGET_ID
        if (appWidgetId == AppWidgetManager.INVALID_APPWIDGET_ID) { finish(); return }
        val store = DevHudWidgetStore(this)
        val root = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL; gravity = Gravity.CENTER_HORIZONTAL; setPadding(48, 48, 48, 48); setBackgroundColor(Color.rgb(29, 37, 48)) }
        root.addView(TextView(this).apply { text = getString(R.string.devhud_widget_choose_deck); textSize = 22f; setTextColor(Color.WHITE) })
        root.addView(TextView(this).apply { text = getString(R.string.devhud_widget_privacy_warning); setTextColor(Color.WHITE); setPadding(0, 24, 0, 24) })
        val ids = store.enabledDeckIds()
        if (ids.isEmpty()) root.addView(TextView(this).apply { text = getString(R.string.devhud_widget_setup); setTextColor(Color.WHITE) })
        ids.forEach { deckId ->
            val configuration = store.configuration(deckId) ?: return@forEach
            root.addView(Button(this).apply {
                text = configuration.optString("name", deckId)
                contentDescription = getString(R.string.devhud_widget_select_deck, text)
                setOnClickListener {
                    if (!store.select(appWidgetId, deckId)) return@setOnClickListener
                    val context = applicationContext
                    DevHudWidgetProvider.renderStored(context, AppWidgetManager.getInstance(context), appWidgetId)
                    DevHudWidgetProvider.scheduleRefresh(context)
                    setResult(RESULT_OK, Intent().putExtra(AppWidgetManager.EXTRA_APPWIDGET_ID, appWidgetId))
                    finish()
                }
            })
        }
        setContentView(ScrollView(this).apply { addView(root) })
    }
}
