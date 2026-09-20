use std::{fmt::Write as _, io::Write};

use chrono::{
    format::{parse_and_remainder, Fixed, Item, Numeric, Parsed, StrftimeItems},
    DateTime, Datelike, LocalResult, NaiveDate, NaiveDateTime, Offset, TimeDelta, TimeZone,
    Timelike, Utc,
};
use chrono_tz::Tz;

use crate::{
    cli::{TimeAdd, TimeArgs, TimeCommand, TimeFrom, TimeTo},
    error::{Code, Error, Result},
    runtime::write,
};

struct Instant {
    value: DateTime<Tz>,
    precision: usize,
}

pub fn run(command: TimeCommand, writer: &mut dyn Write) -> Result<u8> {
    let now = Utc::now();
    let (args, instant) = match &command {
        TimeCommand::Format(args) => (args, parse(args, now)?),
        TimeCommand::Add(args) => (&args.time, add(parse(&args.time, now)?, args)?),
    };
    let output = format(args, instant)?;
    write(writer, output.as_bytes())?;
    write(writer, b"\n")?;
    Ok(0)
}

fn local(zone: Tz, value: NaiveDateTime, parsing: bool) -> Result<DateTime<Tz>> {
    let code = match zone.from_local_datetime(&value) {
        LocalResult::Single(value) => return Ok(value),
        LocalResult::Ambiguous(..) => Code::AmbiguousTime,
        LocalResult::None => Code::NonexistentTime,
    };
    Err(if parsing {
        Error::argument(code)
    } else {
        Error::runtime(code)
    })
}

fn validate_format(format: &str, input: bool) -> Result<Vec<Item<'_>>> {
    let items: Vec<_> = StrftimeItems::new(format).collect();
    if items.iter().any(|item| {
        matches!(item, Item::Error) || input && matches!(item, Item::Fixed(Fixed::TimezoneName))
    }) {
        return Err(Error::argument(Code::InvalidFormat));
    }
    // Chrono's permissive offset is parsing-only. Reject it rather than allowing
    // the formatter's internal panic path to render a dependency error.
    if !input
        && items.iter().any(|item| {
            *item
                == Item::Fixed(match StrftimeItems::new("%#z").next().unwrap() {
                    Item::Fixed(fixed) => fixed,
                    _ => unreachable!(),
                })
        })
    {
        return Err(Error::argument(Code::InvalidFormat));
    }
    Ok(items)
}

fn fraction(value: &str) -> Result<usize> {
    let precision = value
        .split_once('.')
        .map(|(_, tail)| tail.bytes().take_while(u8::is_ascii_digit).count())
        .unwrap_or(0);
    if precision > 9 {
        return Err(Error::argument(Code::InvalidTime));
    }
    Ok(precision)
}

fn parse(args: &TimeArgs, now: DateTime<Utc>) -> Result<Instant> {
    let zone: Tz = args
        .timezone
        .parse()
        .map_err(|_| Error::argument(Code::UnknownTimezone))?;
    let input_items = args
        .input_format
        .as_deref()
        .map(|format| validate_format(format, true))
        .transpose()?;
    if let Some(format) = &args.format {
        validate_format(format, false)?;
    }
    let Some(value) = &args.value else {
        return Ok(Instant {
            value: now.with_timezone(&zone),
            precision: 9,
        });
    };
    let bad = || Error::argument(Code::InvalidTime);
    let (date, precision) = if let Some(items) = input_items {
        let mut parsed = Parsed::new();
        let mut remainder = value.as_str();
        let mut precision = 0;
        for item in &items {
            let before = remainder;
            let previous_nanos = parsed.nanosecond();
            remainder = parse_and_remainder(&mut parsed, remainder, std::iter::once(item))
                .map_err(|_| bad())?;
            let consumed = &before[..before.len() - remainder.len()];
            match item {
                Item::Numeric(Numeric::Nanosecond, _) => precision = 9,
                Item::Fixed(
                    Fixed::Nanosecond
                    | Fixed::Nanosecond3
                    | Fixed::Nanosecond6
                    | Fixed::Nanosecond9
                    | Fixed::RFC3339,
                ) => {
                    precision = fraction(consumed)?;
                }
                Item::Fixed(Fixed::Internal(_)) if parsed.nanosecond() != previous_nanos => {
                    precision = consumed.len();
                }
                _ => {}
            }
        }
        if !remainder.is_empty() || parsed.second() == Some(60) || precision > 9 {
            return Err(bad());
        }
        if parsed.timestamp().is_none() {
            if parsed.hour_div_12().is_none() && parsed.hour_mod_12().is_none() {
                parsed.set_hour(0).map_err(|_| bad())?;
            }
            if parsed.minute().is_none() {
                parsed.set_minute(0).map_err(|_| bad())?;
            }
            if parsed.second().is_none() {
                parsed.set_second(0).map_err(|_| bad())?;
            }
        }
        let date = if parsed.offset().is_some() || parsed.timestamp().is_some() {
            if parsed.offset().is_none() {
                parsed.set_offset(0).map_err(|_| bad())?;
            }
            parsed
                .to_datetime()
                .map_err(|_| bad())?
                .with_timezone(&zone)
        } else {
            local(
                zone,
                parsed.to_naive_datetime_with_offset(0).map_err(|_| bad())?,
                true,
            )?
        };
        (date, precision)
    } else {
        match args.from.unwrap_or_default() {
            TimeFrom::Rfc3339 => {
                let shape = regex::Regex::new(r"^[0-9]{4}-[0-9]{2}-[0-9]{2}[Tt][0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]{1,9})?(?:[Zz]|[+-][0-9]{2}:[0-9]{2})$").unwrap();
                if !shape.is_match(value) {
                    return Err(bad());
                }
                (
                    DateTime::parse_from_rfc3339(value)
                        .map_err(|_| bad())?
                        .with_timezone(&zone),
                    fraction(value)?,
                )
            }
            TimeFrom::Date => {
                if value.len() != 10
                    || !value.bytes().enumerate().all(|(index, byte)| {
                        if index == 4 || index == 7 {
                            byte == b'-'
                        } else {
                            byte.is_ascii_digit()
                        }
                    })
                {
                    return Err(bad());
                }
                let date = NaiveDate::parse_from_str(value, "%Y-%m-%d").map_err(|_| bad())?;
                (local(zone, date.and_hms_opt(0, 0, 0).unwrap(), true)?, 0)
            }
            TimeFrom::UnixS | TimeFrom::UnixMs => {
                let number: i64 = value.parse().map_err(|_| bad())?;
                let millis = matches!(args.from, Some(TimeFrom::UnixMs));
                let (seconds, nanos) = if millis {
                    (
                        number.div_euclid(1000),
                        (number.rem_euclid(1000) * 1_000_000) as u32,
                    )
                } else {
                    (number, 0)
                };
                let date = DateTime::from_timestamp(seconds, nanos).ok_or_else(bad)?;
                (date.with_timezone(&zone), if millis { 3 } else { 0 })
            }
        }
    };
    if !(1..=9999).contains(&date.year()) || date.nanosecond() >= 1_000_000_000 {
        return Err(bad());
    }
    Ok(Instant {
        value: date,
        precision,
    })
}

fn add(mut instant: Instant, args: &TimeAdd) -> Result<Instant> {
    let range = || Error::runtime(Code::TimeRange);
    let zone = instant.value.timezone();
    let months = i128::from(args.years.unwrap_or(0)) * 12 + i128::from(args.months.unwrap_or(0));
    let days = i128::from(args.weeks.unwrap_or(0)) * 7 + i128::from(args.days.unwrap_or(0));
    let seconds = i128::from(args.hours.unwrap_or(0)) * 3600
        + i128::from(args.minutes.unwrap_or(0)) * 60
        + i128::from(args.seconds.unwrap_or(0));
    if months != 0 {
        let current = instant.value.naive_local();
        let index = i128::from(current.year()) * 12 + i128::from(current.month0()) + months;
        let year = index.div_euclid(12);
        let month = index.rem_euclid(12) as u32 + 1;
        if !(1..=9999).contains(&year) {
            return Err(range());
        }
        let date = (1..=current.day())
            .rev()
            .find_map(|day| NaiveDate::from_ymd_opt(year as i32, month, day))
            .ok_or_else(range)?;
        instant.value = local(zone, date.and_time(current.time()), false)?;
    }
    if days != 0 {
        let duration =
            TimeDelta::try_days(days.try_into().map_err(|_| range())?).ok_or_else(range)?;
        let date = instant
            .value
            .naive_local()
            .checked_add_signed(duration)
            .ok_or_else(range)?;
        if !(1..=9999).contains(&date.year()) {
            return Err(range());
        }
        instant.value = local(zone, date, false)?;
    }
    let duration =
        TimeDelta::try_seconds(seconds.try_into().map_err(|_| range())?).ok_or_else(range)?;
    instant.value = instant
        .value
        .checked_add_signed(duration)
        .ok_or_else(range)?;
    if !(1..=9999).contains(&instant.value.year()) {
        return Err(range());
    }
    Ok(instant)
}

fn format(args: &TimeArgs, instant: Instant) -> Result<String> {
    let mut output = String::new();
    if let Some(format) = &args.format {
        let items = validate_format(format, false)?;
        write!(
            &mut output,
            "{}",
            instant.value.format_with_items(items.iter())
        )
        .map_err(|_| Error::argument(Code::InvalidFormat))?;
        return Ok(output);
    }
    match args.to.unwrap_or_default() {
        TimeTo::UnixS => Ok(instant.value.timestamp().to_string()),
        TimeTo::UnixMs => Ok(instant.value.timestamp_millis().to_string()),
        TimeTo::Rfc3339 => {
            // RFC 3339 cannot represent historical sub-minute IANA offsets.
            // Refuse a silently rounded instant; custom %::z retains seconds.
            let offset = instant.value.offset().fix().local_minus_utc();
            if offset % 60 != 0 {
                return Err(Error::runtime(Code::TimeRange));
            }
            write!(&mut output, "{}", instant.value.format("%Y-%m-%dT%H:%M:%S")).unwrap();
            if instant.precision > 0 {
                let nanos = format!("{:09}", instant.value.nanosecond());
                output.push('.');
                output.push_str(&nanos[..instant.precision]);
            }
            if offset == 0 {
                output.push('Z');
            } else {
                write!(&mut output, "{}", instant.value.format("%:z")).unwrap();
            }
            Ok(output)
        }
    }
}

#[cfg(test)]
mod tests {
    use clap::Parser;

    use super::*;
    use crate::cli::{Cli, Command};

    #[test]
    fn omitted_value_uses_injected_current_instant() {
        let cli = Cli::parse_from(["clibox", "time", "format"]);
        let Some(Command::Time {
            command: TimeCommand::Format(args),
        }) = cli.command
        else {
            panic!()
        };
        let now = DateTime::from_timestamp(0, 123_456_789).unwrap();
        assert_eq!(
            format(&args, parse(&args, now).unwrap()).unwrap(),
            "1970-01-01T00:00:00.123456789Z"
        );
    }
}
