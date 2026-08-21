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
import android.content.res.Configuration
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
import java.util.Locale
import java.util.concurrent.CancellationException
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.ExecutorCompletionService
import java.util.concurrent.Executors
import java.util.concurrent.Future
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicReference

private const val resultLimit = 100
private const val staleAfterMillis = 60 * 60 * 1000L
private const val widgetRefreshJobId = 0x444857
private const val repositoryValidationConcurrency = 3
private const val refreshDeadlineMillis = 20_000L
private val widgetExecutor = Executors.newSingleThreadExecutor()
private val repositoryValidationExecutor = Executors.newFixedThreadPool(repositoryValidationConcurrency)
private val widgetDeadlineExecutor = Executors.newSingleThreadScheduledExecutor()

private fun localizedContext(context: Context, configuration: JSONObject?): Context {
    val locale = when (configuration?.optString("language")) {
        "en" -> Locale.ENGLISH
        "ko" -> Locale.KOREAN
        else -> return context
    }
    val resourcesConfiguration = Configuration(context.resources.configuration).apply { setLocale(locale) }
    return context.createConfigurationContext(resourcesConfiguration)
}

internal enum class WidgetRefreshCancellation { DEADLINE, STOPPED, VALIDATION_FAILED }

private class WidgetRefreshCancelled : CancellationException()

internal class WidgetRefreshSession {
    private val deadlineNanos = System.nanoTime() + TimeUnit.MILLISECONDS.toNanos(refreshDeadlineMillis)
    private val cancellation = AtomicReference<WidgetRefreshCancellation?>(null)
    private val connections = ConcurrentHashMap.newKeySet<HttpURLConnection>()
    private val deadlineCancellation = widgetDeadlineExecutor.schedule(
        { cancel(WidgetRefreshCancellation.DEADLINE) },
        (deadlineNanos - System.nanoTime()).coerceAtLeast(0L),
        TimeUnit.NANOSECONDS,
    )

    fun remainingMillis(): Int {
        cancellation.get()?.let { throw WidgetRefreshCancelled() }
        val remainingNanos = deadlineNanos - System.nanoTime()
        if (remainingNanos <= 0) {
            cancel(WidgetRefreshCancellation.DEADLINE)
            throw WidgetRefreshCancelled()
        }
        return TimeUnit.NANOSECONDS.toMillis(remainingNanos).coerceAtLeast(1).coerceAtMost(Int.MAX_VALUE.toLong()).toInt()
    }

    fun track(connection: HttpURLConnection) {
        connections.add(connection)
        if (cancellation.get() != null) {
            connections.remove(connection)
            connection.disconnect()
            throw WidgetRefreshCancelled()
        }
    }

    fun release(connection: HttpURLConnection) {
        connections.remove(connection)
        connection.disconnect()
    }

    fun cancel(reason: WidgetRefreshCancellation) {
        while (true) {
            val current = cancellation.get()
            val next = if (reason == WidgetRefreshCancellation.STOPPED) reason else current ?: reason
            if (current == next || cancellation.compareAndSet(current, next)) break
        }
        connections.forEach { it.disconnect() }
    }

    fun wasStopped(): Boolean = cancellation.get() == WidgetRefreshCancellation.STOPPED

    fun close() {
        deadlineCancellation.cancel(false)
        connections.forEach { it.disconnect() }
        connections.clear()
    }
}

private data class RepositoryValidationFailure(val state: String, val rate: JSONObject?)

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

        internal fun refresh(context: Context, manager: AppWidgetManager, deckId: String?, appWidgetIds: List<Int>, session: WidgetRefreshSession): Boolean {
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
            val credential = store.credential(deckId)
            if (credential == null) {
                appWidgetIds.forEach { renderStored(context, manager, it) }
                return false
            }
            if (credential is WidgetCredential.Missing) {
                val snapshot = failure(configuration, previous, "missing-token", Instant.now().toString(), null)
                val stored = store.replaceSnapshot(snapshot, null)
                val rendered = store.snapshot(deckId)
                renderSelected(context, manager, store, deckId, configuration, rendered, rendered?.optString("state", "missing-token") ?: "missing-token", appWidgetIds)
                return stored
            }
            if (credential is WidgetCredential.Unreadable) {
                val snapshot = failure(configuration, previous, "error", Instant.now().toString(), null)
                val stored = store.replaceSnapshot(snapshot, credential.revision)
                val rendered = store.snapshot(deckId)
                renderSelected(context, manager, store, deckId, configuration, rendered, rendered?.optString("state", "error") ?: "error", appWidgetIds)
                return stored
            }
            check(credential is WidgetCredential.Readable)
            val snapshot = refreshGitHub(configuration, credential.token, previous, session)
            if (session.wasStopped()) return false
            val current = store.configuration(deckId)
            if (current == null || !sameSelection(configuration, current)) {
                appWidgetIds.forEach { renderStored(context, manager, it) }
                return true
            }
            val stored = store.replaceSnapshot(snapshot, credential.revision)
            val rendered = store.snapshot(deckId)
            renderSelected(context, manager, store, deckId, current, rendered, rendered?.optString("state", "error") ?: "error", appWidgetIds)
            return stored
        }

        private fun renderSelected(context: Context, manager: AppWidgetManager, store: DevHudWidgetStore, deckId: String, configuration: JSONObject, snapshot: JSONObject?, forcedState: String, appWidgetIds: List<Int>) {
            appWidgetIds.forEach { appWidgetId ->
                if (store.selectedDeckId(appWidgetId) == deckId) manager.updateAppWidget(appWidgetId, render(context, configuration, snapshot, forcedState))
                else renderStored(context, manager, appWidgetId)
            }
        }

        private fun refreshGitHub(configuration: JSONObject, token: String, previous: JSONObject?, session: WidgetRefreshSession): JSONObject {
            val attemptedAt = Instant.now().toString()
            return try {
                validateRepositories(configuration, token, session)?.let { validation ->
                    return failure(configuration, previous, validation.state, attemptedAt, validation.rate)
                }
                github("/search/issues?q=" + Uri.encode(configuration.getString("query")) + "&per_page=100&page=1", token, session) { connection ->
                    val status = connection.responseCode
                    val responseRate = rate(connection)
                    val rateLimited = status == 429 || status == 403 && (connection.getHeaderField("X-RateLimit-Remaining") == "0" || connection.getHeaderField("Retry-After") != null)
                    if (rateLimited) return@github failure(configuration, previous, "rate-limit", attemptedAt, responseRate)
                    if (status == 401) return@github failure(configuration, previous, "missing-token", attemptedAt, responseRate)
                    if (status == 403 || status == 404) return@github failure(configuration, previous, "permission", attemptedAt, responseRate)
                    if (status !in 200..299) return@github failure(configuration, previous, "error", attemptedAt, responseRate)
                    val payload = connection.inputStream.bufferedReader().use { JSONObject(it.readText()) }
                    if (payload.optBoolean("incomplete_results", false)) return@github failure(configuration, previous, "error", attemptedAt, responseRate)
                    val items = payload.getJSONArray("items")
                    val results = JSONArray()
                    var open = 0; var draft = 0; var merged = 0; var closed = 0
                    for (index in 0 until minOf(items.length(), resultLimit)) {
                        val item = items.getJSONObject(index)
                        val isDraft = item.optBoolean("draft", false)
                        val pullRequest = item.optJSONObject("pull_request")
                        val isMerged = pullRequest?.let { it.has("merged_at") && !it.isNull("merged_at") } == true
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
                        .put("rate", responseRate)
                }
            } catch (_: Exception) { failure(configuration, previous, "error", attemptedAt, null) }
        }

        private fun validateRepositories(configuration: JSONObject, token: String, session: WidgetRefreshSession): RepositoryValidationFailure? {
            val repositories = configuration.getJSONArray("repositories")
            val completion = ExecutorCompletionService<RepositoryValidationFailure?>(repositoryValidationExecutor)
            val pending = mutableListOf<Future<RepositoryValidationFailure?>>()
            var nextIndex = 0
            var completed = 0
            fun submitNext() {
                val repository = repositories.getJSONObject(nextIndex++)
                pending += completion.submit {
                    validateRepository(repository, configuration.getString("profileKind"), token, session)
                }
            }
            repeat(minOf(repositoryValidationConcurrency, repositories.length())) { submitNext() }
            try {
                while (completed < repositories.length()) {
                    val completedFuture = completion.poll(session.remainingMillis().toLong(), TimeUnit.MILLISECONDS)
                        ?: run {
                            session.cancel(WidgetRefreshCancellation.DEADLINE)
                            return RepositoryValidationFailure("error", null)
                        }
                    pending.remove(completedFuture)
                    completed += 1
                    val validation = try { completedFuture.get() } catch (_: Exception) { RepositoryValidationFailure("error", null) }
                    if (validation != null) {
                        session.cancel(WidgetRefreshCancellation.VALIDATION_FAILED)
                        return validation
                    }
                    if (nextIndex < repositories.length()) submitNext()
                }
                return null
            } catch (_: WidgetRefreshCancelled) {
                return RepositoryValidationFailure("error", null)
            } finally {
                pending.forEach { it.cancel(true) }
            }
        }

        private fun validateRepository(repository: JSONObject, profileKind: String, token: String, session: WidgetRefreshSession): RepositoryValidationFailure? {
            return try {
                val path = "/repos/${repository.getString("owner")}/${repository.getString("name")}"
                var neverPushed = false
                val metadataFailure = github(path, token, session) { metadata ->
                    responseFailure(metadata)?.let { return@github RepositoryValidationFailure(it, rate(metadata)) }
                    if (profileKind == "classic") {
                        val scopes = metadata.getHeaderField("X-OAuth-Scopes").orEmpty().split(",").map { it.trim() }.toSet()
                        if ("repo" !in scopes) return@github RepositoryValidationFailure("permission", rate(metadata))
                    }
                    val metadataPayload = metadata.inputStream.bufferedReader().use { JSONObject(it.readText()) }
                    neverPushed = metadataPayload.isNull("pushed_at")
                    null
                }
                if (metadataFailure != null) return metadataFailure
                for (suffix in listOf("/pulls?state=open&per_page=1", "/issues?state=open&per_page=1")) {
                    val accessFailure = github(path + suffix, token, session) { access ->
                        responseFailure(access)?.let { RepositoryValidationFailure(it, rate(access)) }
                            ?: run { access.inputStream.close(); null }
                    }
                    if (accessFailure != null) return accessFailure
                }
                val contentsFailure = github(path + "/contents", token, session) { contents ->
                    val validation = if (contents.responseCode != 404 || !neverPushed) responseFailure(contents) else null
                    if (validation != null) RepositoryValidationFailure(validation, rate(contents))
                    else { if (contents.responseCode in 200..299) contents.inputStream.close() else contents.errorStream?.close(); null }
                }
                if (contentsFailure != null) return contentsFailure
                if (profileKind == "fine-grained") {
                    val probeFailure = github(path + "/issues", token, session, "POST", "{}") { probe ->
                        if (probe.responseCode == 422) { probe.errorStream?.close(); null }
                        else responseFailure(probe)?.let { RepositoryValidationFailure(it, rate(probe)) }
                            ?: RepositoryValidationFailure("error", rate(probe))
                    }
                    if (probeFailure != null) return probeFailure
                }
                null
            } catch (_: Exception) { RepositoryValidationFailure("error", null) }
        }

        private inline fun <T> github(path: String, token: String, session: WidgetRefreshSession, method: String = "GET", body: String? = null, consume: (HttpURLConnection) -> T): T {
            val connection = URL("https://api.github.com$path").openConnection() as HttpURLConnection
            session.track(connection)
            return try {
                connection.connectTimeout = minOf(15_000, session.remainingMillis())
                connection.readTimeout = minOf(20_000, session.remainingMillis())
                connection.requestMethod = method
                connection.setRequestProperty("Accept", "application/vnd.github+json")
                connection.setRequestProperty("Authorization", "Bearer $token")
                connection.setRequestProperty("X-GitHub-Api-Version", "2026-03-10")
                if (body != null) {
                    connection.doOutput = true
                    connection.setRequestProperty("Content-Type", "application/json")
                    connection.outputStream.use { it.write(body.toByteArray(Charsets.UTF_8)) }
                }
                connection.responseCode
                consume(connection)
            } finally { session.release(connection) }
        }

        private fun responseFailure(connection: HttpURLConnection): String? {
            val status = connection.responseCode
            if (status in 200..299) return null
            if (status == 429 || status == 403 && (connection.getHeaderField("X-RateLimit-Remaining") == "0" || connection.getHeaderField("Retry-After") != null)) return "rate-limit"
            if (status == 401) return "missing-token"
            if (status == 403 || status == 404) return "permission"
            return "error"
        }

        private fun failure(configuration: JSONObject, previous: JSONObject?, state: String, attemptedAt: String, responseRate: JSONObject?): JSONObject {
            val retained = previous?.takeIf { it.optString("query") == configuration.getString("query") } ?: JSONObject().put("version", 1).put("deckId", configuration.getString("deckId")).put("query", configuration.getString("query"))
                .put("counts", JSONObject().put("total", 0).put("open", 0).put("draft", 0).put("merged", 0).put("closed", 0).put("bounded", false))
                .put("results", JSONArray()).put("lastSuccessfulAt", JSONObject.NULL)
            retained.put("state", state).put("lastAttemptedAt", attemptedAt)
            if (responseRate != null) retained.put("rate", responseRate)
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
            val copyContext = localizedContext(context, configuration)
            val views = RemoteViews(context.packageName, R.layout.devhud_widget)
            val deckName = configuration?.optString("name")?.takeIf(String::isNotBlank) ?: copyContext.getString(R.string.devhud_widget_setup)
            views.setTextViewText(R.id.widget_deck_name, deckName)
            val counts = snapshot?.optJSONObject("counts")
            val countsText = if (counts == null) copyContext.getString(R.string.devhud_widget_no_results) else copyContext.getString(R.string.devhud_widget_counts,
                counts.optInt("total"), counts.optInt("open"), counts.optInt("draft"), counts.optInt("merged"), counts.optInt("closed")) + if (counts.optBoolean("bounded")) copyContext.getString(R.string.devhud_widget_first_hundred) else ""
            views.setTextViewText(R.id.widget_counts, countsText)
            val results = snapshot?.optJSONArray("results")
            val previews = (0 until minOf(results?.length() ?: 0, 3)).joinToString("\n") { index ->
                val item = results!!.getJSONObject(index)
                "${item.optString("repository")}#${item.optInt("number")} · ${item.optString("title")}"
            }
            views.setTextViewText(R.id.widget_results, previews.ifEmpty { copyContext.getString(R.string.devhud_widget_no_results) })
            val lastSuccess = snapshot?.optString("lastSuccessfulAt")?.takeIf { it.isNotBlank() && it != "null" }
            val stale = lastSuccess != null && System.currentTimeMillis() - runCatching { Instant.parse(lastSuccess).toEpochMilli() }.getOrDefault(0) >= staleAfterMillis
            val state = if (forcedState == "fresh" && stale) "stale" else forcedState
            val stateLabel = when (state) {
                "stale" -> R.string.devhud_widget_stale
                "missing-token" -> R.string.devhud_widget_missing_token
                "rate-limit" -> R.string.devhud_widget_rate_limit
                "permission" -> R.string.devhud_widget_permission
                "error" -> R.string.devhud_widget_error
                else -> R.string.devhud_widget_fresh
            }
            val stateText = copyContext.getString(stateLabel) + if (stale && state != "stale") " · ${copyContext.getString(R.string.devhud_widget_stale)}" else ""
            views.setTextViewText(R.id.widget_status, copyContext.getString(R.string.devhud_widget_status, stateText, lastSuccess ?: copyContext.getString(R.string.devhud_widget_never)))
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
    private class WidgetRefreshRun(val parameters: JobParameters) {
        private val stopped = AtomicBoolean(false)
        private val activeSession = AtomicReference<WidgetRefreshSession?>(null)

        fun isStopped(): Boolean = stopped.get()

        fun attach(session: WidgetRefreshSession) {
            activeSession.set(session)
            if (stopped.get()) session.cancel(WidgetRefreshCancellation.STOPPED)
        }

        fun detach(session: WidgetRefreshSession) {
            activeSession.compareAndSet(session, null)
        }

        fun stop() {
            stopped.set(true)
            activeSession.get()?.cancel(WidgetRefreshCancellation.STOPPED)
        }
    }

    private val lifecycleLock = Any()
    private var activeRun: WidgetRefreshRun? = null

    override fun onStartJob(parameters: JobParameters): Boolean {
        val run = WidgetRefreshRun(parameters)
        synchronized(lifecycleLock) {
            activeRun?.stop()
            activeRun = run
        }
        widgetExecutor.execute {
            var retry = false
            try {
                val manager = AppWidgetManager.getInstance(applicationContext)
                val component = ComponentName(applicationContext, DevHudWidgetProvider::class.java)
                val store = DevHudWidgetStore(applicationContext)
                for ((deckId, appWidgetIds) in manager.getAppWidgetIds(component).groupBy { store.selectedDeckId(it) }) {
                    if (run.isStopped()) { retry = true; break }
                    val session = WidgetRefreshSession()
                    run.attach(session)
                    try {
                        if (!DevHudWidgetProvider.refresh(applicationContext, manager, deckId, appWidgetIds, session)) retry = true
                    } finally {
                        run.detach(session)
                        session.close()
                    }
                }
            } catch (_: Exception) { retry = true }
            synchronized(lifecycleLock) {
                if (!run.isStopped() && activeRun === run) {
                    activeRun = null
                    jobFinished(run.parameters, retry)
                }
            }
        }
        return true
    }

    override fun onStopJob(parameters: JobParameters): Boolean {
        synchronized(lifecycleLock) {
            activeRun?.stop()
            activeRun = null
        }
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
        val configurations = store.enabledDeckIds().mapNotNull(store::configuration)
        val copyContext = localizedContext(this, configurations.firstOrNull())
        val root = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL; gravity = Gravity.CENTER_HORIZONTAL; setPadding(48, 48, 48, 48); setBackgroundColor(Color.rgb(29, 37, 48)) }
        root.addView(TextView(this).apply { text = copyContext.getString(R.string.devhud_widget_choose_deck); textSize = 22f; setTextColor(Color.WHITE) })
        root.addView(TextView(this).apply { text = copyContext.getString(R.string.devhud_widget_privacy_warning); setTextColor(Color.WHITE); setPadding(0, 24, 0, 24) })
        if (configurations.isEmpty()) root.addView(TextView(this).apply { text = copyContext.getString(R.string.devhud_widget_setup); setTextColor(Color.WHITE) })
        configurations.forEach { configuration ->
            val deckId = configuration.getString("deckId")
            root.addView(Button(this).apply {
                text = configuration.optString("name", deckId)
                contentDescription = copyContext.getString(R.string.devhud_widget_select_deck, text)
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
