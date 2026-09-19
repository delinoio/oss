use std::str::FromStr;

use anyhow::{ensure, Context, Result};
use chrono::{DateTime, Timelike, Utc};

pub struct IntervalClock {
    period: std::time::Duration,
    next: std::time::Instant,
}
impl IntervalClock {
    pub fn new(period: std::time::Duration, now: std::time::Instant) -> Self {
        Self {
            period,
            next: now + period,
        }
    }

    pub fn tick(&mut self, now: std::time::Instant) -> bool {
        if now < self.next {
            return false;
        }
        self.next = now + self.period;
        true
    }
}

pub fn parse_cron(expression: &str) -> Result<cron::Schedule> {
    ensure!(
        expression.split_whitespace().count() == 5,
        "cron requires five fields"
    );
    let mut fields: Vec<_> = expression.split_whitespace().map(str::to_owned).collect();
    fields[4] = weekdays(&fields[4])?;
    cron::Schedule::from_str(&format!("0 {} *", fields.join(" ")))
        .context("invalid cron expression")
}

// The underlying parser uses Sunday=1. The public five-field contract follows
// conventional cron Sunday=0 (also 7); convert the bounded weekday set
// explicitly.
fn weekdays(field: &str) -> Result<String> {
    let number = |text: &str| -> Result<u32> {
        Ok(match text.to_ascii_uppercase().as_str() {
            "SUN" => 0,
            "MON" => 1,
            "TUE" => 2,
            "WED" => 3,
            "THU" => 4,
            "FRI" => 5,
            "SAT" => 6,
            _ => {
                let value: u32 = text.parse().context("invalid cron weekday")?;
                ensure!(value <= 7, "cron weekday outside 0..7");
                value
            }
        })
    };
    let mut days = std::collections::BTreeSet::new();
    for item in field.split(',') {
        let (range, step) = if let Some((range, step)) = item.split_once('/') {
            let step: usize = step.parse().context("invalid weekday step")?;
            ensure!(step > 0, "weekday step must be positive");
            (range, step)
        } else {
            (item, 1)
        };
        let (start, end) = if range == "*" {
            (0, 6)
        } else if let Some((start, end)) = range.split_once('-') {
            (number(start)?, number(end)?)
        } else {
            let start = number(range)?;
            (start, if item.contains('/') { 7 } else { start })
        };
        ensure!(start <= end, "descending cron weekday range");
        for day in (start..=end).step_by(step) {
            days.insert(day % 7 + 1);
        }
    }
    Ok(days
        .into_iter()
        .map(|day| day.to_string())
        .collect::<Vec<_>>()
        .join(","))
}

/// A repeated DST minute is one occurrence; clock jumps never replay missed
/// ticks.
pub struct CronClock {
    schedules: Vec<cron::Schedule>,
    zone: chrono_tz::Tz,
    last_wall: Option<chrono::NaiveDateTime>,
}
impl CronClock {
    pub fn new(expression: &str, zone: &str) -> Result<Self> {
        let mut schedules = vec![parse_cron(expression)?];
        let fields: Vec<_> = expression.split_whitespace().collect();
        // Restricted day-of-month and weekday fields form a union in five-field
        // cron. The underlying seven-field parser intersects them instead.
        if fields[2] != "*" && fields[4] != "*" {
            let mut month_day = fields.clone();
            month_day[4] = "*";
            let mut week_day = fields;
            week_day[2] = "*";
            schedules = vec![
                parse_cron(&month_day.join(" "))?,
                parse_cron(&week_day.join(" "))?,
            ];
        }
        Ok(Self {
            schedules,
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
        self.schedules
            .iter()
            .any(|schedule| schedule.includes(local))
    }
}
