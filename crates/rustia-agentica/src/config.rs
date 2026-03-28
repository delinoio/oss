use std::env;

/// Default maximum step count for one conversation turn.
pub const DEFAULT_MAX_STEPS: usize = 3;

/// Runtime configuration for [`crate::MicroAgentica`].
#[derive(Debug, Clone)]
pub struct MicroAgenticaConfig {
    pub locale: String,
    pub timezone: String,
    pub max_steps: usize,
    pub execute_prompt: String,
    pub common_prompt_template: String,
}

impl Default for MicroAgenticaConfig {
    fn default() -> Self {
        Self {
            locale: default_locale(),
            timezone: default_timezone(),
            max_steps: DEFAULT_MAX_STEPS,
            execute_prompt: include_str!("../prompts/execute.md").to_owned(),
            common_prompt_template: include_str!("../prompts/common.md").to_owned(),
        }
    }
}

impl MicroAgenticaConfig {
    /// Renders the full system prompt from execute/common prompt templates.
    pub fn render_system_prompt(&self) -> String {
        let common = self
            .common_prompt_template
            .replace("${locale}", &self.locale)
            .replace("${timezone}", &self.timezone)
            .replace("${datetime}", &current_datetime_iso());

        format!("{}\n\n{}", self.execute_prompt.trim(), common.trim())
    }
}

fn current_datetime_iso() -> String {
    chrono::Utc::now().to_rfc3339()
}

fn default_locale() -> String {
    env::var("LANG")
        .ok()
        .and_then(|lang| lang.split('.').next().map(str::to_owned))
        .filter(|value| !value.trim().is_empty())
        .unwrap_or_else(|| "en-US".to_owned())
}

fn default_timezone() -> String {
    env::var("TZ")
        .ok()
        .filter(|value| !value.trim().is_empty())
        .unwrap_or_else(|| "UTC".to_owned())
}
