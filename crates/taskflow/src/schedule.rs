use std::str::FromStr;

use anyhow::{ensure, Context, Result};
use chrono::{DateTime, Timelike, Utc};

pub fn parse_cron(expression: &str) -> Result<cron::Schedule> {
    ensure!(
        expression.split_whitespace().count() == 5,
        "cron requires five fields"
    );
    cron::Schedule::from_str(&format!("0 {expression} *")).context("invalid cron expression")
}

/// A repeated DST minute is one occurrence; clock jumps never replay missed
/// ticks.
pub struct CronClock {
    schedule: cron::Schedule,
    zone: chrono_tz::Tz,
    last_wall: Option<chrono::NaiveDateTime>,
}
impl CronClock {
    pub fn new(expression: &str, zone: &str) -> Result<Self> {
        Ok(Self {
            schedule: parse_cron(expression)?,
            zone: zone.parse()?,
            last_wall: None,
        })
    }

    pub fn tick(&mut self, now: DateTime<Utc>) -> bool {
        // Round in UTC: rounding local time during a repeated DST hour is
        // ambiguous and chrono correctly refuses to choose an offset for it.
        let local = now
            .with_second(0)
            .unwrap()
            .with_nanosecond(0)
            .unwrap()
            .with_timezone(&self.zone);
        let wall = local.naive_local();
        if self.last_wall.is_some_and(|previous| previous >= wall) {
            return false;
        }
        self.last_wall = Some(wall);
        self.schedule.includes(local)
    }
}
