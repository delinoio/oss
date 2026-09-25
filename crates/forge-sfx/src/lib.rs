//! Bounded offline procedural SFX synthesis. No devices, fonts, files or
//! network.
use std::{
    collections::HashSet,
    f64::consts::{FRAC_PI_2, TAU},
};

use forge_tree_doc::{ErrorCode, Result, cancellation::checkpoint, error};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

pub const MAX_SECONDS: f64 = 30.0;
pub const MAX_LAYERS: usize = 256;
pub const MAX_VOICE_SAMPLES: usize = 16_000_000;

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Sound {
    pub id: Uuid,
    pub sample_rate: u32,
    pub channels: u16,
    pub duration: f64,
    pub gain: f64,
    pub layers: Vec<Layer>,
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Layer {
    pub id: Uuid,
    pub start: f64,
    pub duration: f64,
    pub gain: f64,
    pub pan: f64,
    pub attack: f64,
    pub release: f64,
    pub decay: Option<f64>,
    pub source: Source,
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
pub enum Source {
    Noise {
        seed: u32,
        low_pass: Option<f64>,
        high_pass: Option<f64>,
    },
    Tone {
        frequency: f64,
        end_frequency: f64,
        waveform: Waveform,
    },
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum Waveform {
    Sine,
    Triangle,
}

fn bounded(value: f64, min: f64, max: f64) -> bool {
    value.is_finite() && value >= min && value <= max
}

pub fn validate(sound: &Sound) -> Result<()> {
    checkpoint()?;
    if sound.layers.len() > MAX_LAYERS {
        return error(
            ErrorCode::ResourceLimit,
            "layers",
            "SFX layer limit exceeded",
        );
    }
    let rate = f64::from(sound.sample_rate);
    if !matches!(sound.sample_rate, 44100 | 48000)
        || !matches!(sound.channels, 1 | 2)
        || !bounded(sound.duration, 2.0 / rate, MAX_SECONDS)
        || !bounded(sound.gain, 0.0, 4.0)
        || sound.layers.is_empty()
        || sound.id.get_version_num() != 7
    {
        return error(ErrorCode::InvalidField, "", "Invalid SFX sound");
    }
    let mut ids = HashSet::from([sound.id]);
    let mut work = 0_usize;
    for layer in &sound.layers {
        checkpoint()?;
        if layer.id.get_version_num() != 7
            || !ids.insert(layer.id)
            || !bounded(layer.start, 0.0, sound.duration)
            || !bounded(layer.duration, 2.0 / rate, sound.duration)
            || layer.start + layer.duration > sound.duration + 1e-9
            || !bounded(layer.gain, 0.0, 4.0)
            || !bounded(layer.pan, -1.0, 1.0)
            || sound.channels == 1 && layer.pan != 0.0
            || !bounded(layer.attack, 1.0 / rate, layer.duration)
            || !bounded(layer.release, 1.0 / rate, layer.duration)
            || layer.attack + layer.release > layer.duration + 1e-9
            || layer
                .decay
                .is_some_and(|v| !bounded(v, 1.0 / rate, MAX_SECONDS))
        {
            return error(ErrorCode::InvalidField, "layers", "Invalid SFX layer");
        }
        match layer.source {
            Source::Noise {
                seed,
                low_pass,
                high_pass,
            } => {
                if seed == 0
                    || low_pass.is_some_and(|v| !bounded(v, 20.0, rate / 2.0 - 1.0))
                    || high_pass.is_some_and(|v| !bounded(v, 20.0, rate / 2.0 - 1.0))
                    || matches!((low_pass, high_pass), (Some(low), Some(high)) if high >= low)
                {
                    return error(
                        ErrorCode::InvalidField,
                        "layers/source",
                        "Invalid noise source",
                    );
                }
            }
            Source::Tone {
                frequency,
                end_frequency,
                ..
            } => {
                if !bounded(frequency, 20.0, rate / 2.0 - 1.0)
                    || !bounded(end_frequency, 20.0, rate / 2.0 - 1.0)
                {
                    return error(
                        ErrorCode::InvalidField,
                        "layers/source",
                        "Invalid tone source",
                    );
                }
            }
        }
        work += (layer.duration * rate).round() as usize;
        if work > MAX_VOICE_SAMPLES {
            return error(
                ErrorCode::ResourceLimit,
                "layers",
                "SFX synthesis budget exceeded",
            );
        }
    }
    Ok(())
}

/// Generate signed little-endian PCM16 RIFF/WAVE. Quantize each boundary to its
/// nearest sample; clamp the final layer frame to the already validated sound.
pub fn generate(sound: &Sound) -> Result<Vec<u8>> {
    validate(sound)?;
    let rate = f64::from(sound.sample_rate);
    let frames = (sound.duration * rate).round() as usize;
    let channels = usize::from(sound.channels);
    let mut mix = vec![0.0_f64; frames * channels];
    for layer in &sound.layers {
        checkpoint()?;
        let offset = (layer.start * rate).round() as usize;
        let count = ((layer.duration * rate).round() as usize).min(frames - offset);
        let left = ((layer.pan + 1.0) * std::f64::consts::FRAC_PI_4).cos();
        let right = ((layer.pan + 1.0) * std::f64::consts::FRAC_PI_4).sin();
        let mut state = match layer.source {
            Source::Noise { seed, .. } => seed,
            _ => 1,
        };
        let mut low = 0.0;
        let mut high_low = 0.0;
        let (low_alpha, high_alpha) = match layer.source {
            Source::Noise {
                low_pass,
                high_pass,
                ..
            } => (
                low_pass.map(|hz| 1.0 - (-TAU * hz / rate).exp()),
                high_pass.map(|hz| 1.0 - (-TAU * hz / rate).exp()),
            ),
            _ => (None, None),
        };
        for i in 0..count {
            if i % 4096 == 0 {
                checkpoint()?;
            }
            let t = i as f64 / rate;
            let sample = match layer.source {
                Source::Noise { .. } => {
                    state ^= state << 13;
                    state ^= state >> 17;
                    state ^= state << 5;
                    let mut sample = f64::from(state) / f64::from(u32::MAX) * 2.0 - 1.0;
                    if let Some(alpha) = low_alpha {
                        low += alpha * (sample - low);
                        sample = low;
                    }
                    if let Some(alpha) = high_alpha {
                        high_low += alpha * (sample - high_low);
                        sample -= high_low;
                    }
                    sample
                }
                Source::Tone {
                    frequency,
                    end_frequency,
                    ref waveform,
                } => {
                    let phase = TAU
                        * (frequency * t
                            + (end_frequency - frequency) * t * t / (2.0 * layer.duration));
                    match waveform {
                        Waveform::Sine => phase.sin(),
                        // Additive triangle omits harmonics at or above Nyquist,
                        // avoiding the aliases of a naive piecewise oscillator.
                        Waveform::Triangle => {
                            let hz = frequency + (end_frequency - frequency) * t / layer.duration;
                            (1..=31)
                                .step_by(2)
                                .take_while(|n| f64::from(*n) * hz < rate / 2.0)
                                .map(|n| {
                                    let sign = if (n / 2) % 2 == 0 { 1.0 } else { -1.0 };
                                    sign * (phase * f64::from(n)).sin() / f64::from(n * n)
                                })
                                .sum::<f64>()
                                / (FRAC_PI_2 * FRAC_PI_2)
                                * 2.0
                        }
                    }
                }
            };
            // The first and final samples are exactly zero even for noise,
            // preventing discontinuities when the game plays a one-shot asset.
            let envelope = (t / layer.attack).min(1.0)
                * (((count - 1 - i) as f64 / rate) / layer.release).min(1.0)
                * layer.decay.map_or(1.0, |decay| (-t / decay).exp());
            let value = sample * envelope * layer.gain * sound.gain;
            let at = (offset + i) * channels;
            if channels == 1 {
                mix[at] += value;
            } else {
                mix[at] += value * left;
                mix[at + 1] += value * right;
            }
        }
    }
    let mut peak = 0.0_f64;
    for chunk in mix.chunks(4096) {
        checkpoint()?;
        peak = chunk
            .iter()
            .fold(peak, |peak, sample| peak.max(sample.abs()));
    }
    // Attenuate the entire mix only when necessary, retaining relative layer
    // levels and stereo balance without boosting quiet tails or silence.
    let scale = if peak > 0.95 { 0.95 / peak } else { 1.0 };
    let data_size = (mix.len() * 2) as u32;
    let mut bytes = Vec::with_capacity(data_size as usize + 44);
    bytes.extend_from_slice(b"RIFF");
    bytes.extend_from_slice(&(data_size + 36).to_le_bytes());
    bytes.extend_from_slice(b"WAVEfmt ");
    bytes.extend_from_slice(&16_u32.to_le_bytes());
    bytes.extend_from_slice(&1_u16.to_le_bytes());
    bytes.extend_from_slice(&sound.channels.to_le_bytes());
    bytes.extend_from_slice(&sound.sample_rate.to_le_bytes());
    bytes.extend_from_slice(&(sound.sample_rate * u32::from(sound.channels) * 2).to_le_bytes());
    bytes.extend_from_slice(&(sound.channels * 2).to_le_bytes());
    bytes.extend_from_slice(&16_u16.to_le_bytes());
    bytes.extend_from_slice(b"data");
    bytes.extend_from_slice(&data_size.to_le_bytes());
    for chunk in mix.chunks(4096) {
        checkpoint()?;
        for sample in chunk {
            bytes.extend_from_slice(&((sample * scale * 32767.0).round() as i16).to_le_bytes());
        }
    }
    Ok(bytes)
}
