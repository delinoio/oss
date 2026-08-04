package dev.deli.devhud.widget

import android.app.Activity
import android.appwidget.AppWidgetManager
import android.content.ComponentName
import android.content.Intent
import android.os.Bundle
import android.view.View
import android.widget.Button
import android.widget.LinearLayout
import android.widget.RadioButton
import android.widget.RadioGroup
import android.widget.TextView
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

internal fun isDevHudWidgetConfigurationRequest(
    action: String?,
    appWidgetId: Int,
    providerPackage: String?,
    providerClassName: String?,
    applicationPackage: String,
): Boolean =
    action == AppWidgetManager.ACTION_APPWIDGET_CONFIGURE &&
        appWidgetId != AppWidgetManager.INVALID_APPWIDGET_ID &&
        providerPackage == applicationPackage &&
        providerClassName == DevHudWidgetProvider::class.java.name

class DevHudWidgetConfigurationActivity : Activity() {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main)
    private var appWidgetId = AppWidgetManager.INVALID_APPWIDGET_ID

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setResult(RESULT_CANCELED)
        appWidgetId = intent?.getIntExtra(
            AppWidgetManager.EXTRA_APPWIDGET_ID,
            AppWidgetManager.INVALID_APPWIDGET_ID,
        ) ?: AppWidgetManager.INVALID_APPWIDGET_ID
        val provider = if (appWidgetId == AppWidgetManager.INVALID_APPWIDGET_ID) {
            null
        } else {
            AppWidgetManager.getInstance(this).getAppWidgetInfo(appWidgetId)?.provider
        }
        if (!isDevHudWidgetConfigurationRequest(
                intent?.action,
                appWidgetId,
                provider?.packageName,
                provider?.className,
                packageName,
            )
        ) {
            finish()
            return
        }
        title = getString(R.string.devhud_widget_configure_title)
        setContentView(container().apply {
            addView(TextView(this@DevHudWidgetConfigurationActivity).apply {
                setText(R.string.devhud_widget_loading)
            })
        })
        scope.launch {
            val widgets = try {
                withContext(Dispatchers.IO) {
                    AndroidWidgetSharedDataAdapter.live(applicationContext)
                        .readRecord().configuration.widgets
                }
            } catch (_: WidgetConfigurationException) {
                emptyList()
            }
            renderChoices(widgets)
        }
    }

    override fun onDestroy() {
        scope.cancel()
        super.onDestroy()
    }

    private fun renderChoices(widgets: List<DeckWidgetInstance>) {
        val manager = AppWidgetManager.getInstance(this)
        val family = androidWidgetFamily(manager.getAppWidgetOptions(appWidgetId))
        val compatible = widgets.filter { it.family == family }
        val root = container()
        root.addView(TextView(this).apply {
            setText(R.string.devhud_widget_configure_description)
        })
        if (compatible.isEmpty()) {
            root.addView(TextView(this).apply {
                setText(R.string.devhud_widget_no_configurations)
            })
            root.addView(Button(this).apply {
                setText(android.R.string.cancel)
                setOnClickListener { finish() }
            })
            setContentView(root)
            return
        }

        val choices = RadioGroup(this).apply { orientation = RadioGroup.VERTICAL }
        val widgetsByButton = mutableMapOf<Int, DeckWidgetInstance>()
        val storedId = DeckWidgetSelections.get(this, appWidgetId)
        compatible.forEachIndexed { index, widget ->
            val buttonId = View.generateViewId()
            widgetsByButton[buttonId] = widget
            choices.addView(RadioButton(this).apply {
                id = buttonId
                text = getString(
                    R.string.devhud_widget_configuration_option,
                    index + 1,
                    widget.viewId.takeLast(8),
                    widget.snapshot.matchingCount,
                )
                isChecked = widget.widgetId == storedId
            })
        }
        root.addView(choices)
        val save = Button(this).apply {
            setText(R.string.devhud_widget_save)
            isEnabled = choices.checkedRadioButtonId != -1
            setOnClickListener {
                val selected = widgetsByButton[choices.checkedRadioButtonId] ?: return@setOnClickListener
                DeckWidgetSelections.set(this@DevHudWidgetConfigurationActivity, appWidgetId, selected.widgetId)
                setResult(RESULT_OK, Intent().putExtra(AppWidgetManager.EXTRA_APPWIDGET_ID, appWidgetId))
                sendBroadcast(Intent(AppWidgetManager.ACTION_APPWIDGET_UPDATE).apply {
                    component = ComponentName(
                        this@DevHudWidgetConfigurationActivity,
                        DevHudWidgetProvider::class.java,
                    )
                    putExtra(AppWidgetManager.EXTRA_APPWIDGET_IDS, intArrayOf(appWidgetId))
                })
                finish()
            }
        }
        choices.setOnCheckedChangeListener { _, _ -> save.isEnabled = true }
        root.addView(LinearLayout(this).apply {
            orientation = LinearLayout.HORIZONTAL
            addView(Button(this@DevHudWidgetConfigurationActivity).apply {
                setText(android.R.string.cancel)
                setOnClickListener { finish() }
            })
            addView(save)
        })
        setContentView(root)
    }

    private fun container() = LinearLayout(this).apply {
        orientation = LinearLayout.VERTICAL
        val padding = (24 * resources.displayMetrics.density).toInt()
        setPadding(padding, padding, padding, padding)
    }
}
