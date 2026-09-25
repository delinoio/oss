use std::sync::{Arc, atomic::AtomicBool};

use forge_sfx::{Layer, Sound, Source, Waveform, generate, validate};
use forge_tree_doc::{ErrorCode, cancellation::with_cancellation};
use uuid::Uuid;

fn fixture() -> Sound {
    Sound {
        id: Uuid::now_v7(),
        sample_rate: 48000,
        channels: 1,
        duration: 0.25,
        gain: 1.0,
        layers: vec![Layer {
            id: Uuid::now_v7(),
            start: 0.025,
            duration: 0.2,
            gain: 0.4,
            pan: 0.0,
            attack: 0.001,
            release: 0.02,
            decay: None,
            source: Source::Tone {
                frequency: 1000.0,
                end_frequency: 1000.0,
                waveform: Waveform::Sine,
            },
        }],
    }
}
fn samples(bytes: &[u8]) -> Vec<i16> {
    bytes[44..]
        .chunks_exact(2)
        .map(|b| i16::from_le_bytes([b[0], b[1]]))
        .collect()
}

#[test]
fn pcm_header_timing_frequency_and_silence_match_the_declared_sound() {
    for rate in [44100, 48000] {
        let mut sound = fixture();
        sound.sample_rate = rate;
        let wav = generate(&sound).unwrap();
        assert_eq!(&wav[0..4], b"RIFF");
        assert_eq!(&wav[8..16], b"WAVEfmt ");
        assert_eq!(&wav[36..40], b"data");
        assert_eq!(
            u32::from_le_bytes(wav[4..8].try_into().unwrap()) as usize,
            wav.len() - 8
        );
        assert_eq!(u16::from_le_bytes(wav[20..22].try_into().unwrap()), 1);
        assert_eq!(u32::from_le_bytes(wav[24..28].try_into().unwrap()), rate);
        assert_eq!(
            u32::from_le_bytes(wav[28..32].try_into().unwrap()),
            rate * 2
        );
        assert_eq!(u16::from_le_bytes(wav[32..34].try_into().unwrap()), 2);
        assert_eq!(u16::from_le_bytes(wav[34..36].try_into().unwrap()), 16);
        let pcm = samples(&wav);
        assert_eq!(pcm.len(), rate as usize / 4);
        assert!(
            pcm[..(0.025 * f64::from(rate)).round() as usize]
                .iter()
                .all(|s| *s == 0)
        );
        assert!(
            pcm[(0.23 * f64::from(rate)) as usize..]
                .iter()
                .all(|s| *s == 0)
        );
        let steady = &pcm[(0.05 * f64::from(rate)) as usize..(0.15 * f64::from(rate)) as usize];
        let crossings = steady.windows(2).filter(|p| p[0] <= 0 && p[1] > 0).count();
        assert!(
            (99..=101).contains(&crossings),
            "1 kHz tone must have 100 cycles in 100 ms"
        );
    }
}

#[test]
fn seeded_noise_is_repeatable_and_stereo_pan_preserves_silence() {
    let mut sound = fixture();
    sound.channels = 2;
    sound.layers[0].pan = -1.0;
    sound.layers[0].source = Source::Noise {
        seed: 815,
        low_pass: Some(6000.0),
        high_pass: Some(100.0),
    };
    let first = generate(&sound).unwrap();
    assert_eq!(first, generate(&sound).unwrap());
    let pcm = samples(&first);
    assert!(pcm.chunks_exact(2).all(|frame| frame[1] == 0));
    assert!(pcm.iter().any(|sample| *sample != 0));
    sound.layers[0].source = Source::Noise {
        seed: 816,
        low_pass: Some(6000.0),
        high_pass: Some(100.0),
    };
    assert_ne!(first, generate(&sound).unwrap());
    sound.layers[0].pan = 1.0;
    assert!(
        samples(&generate(&sound).unwrap())
            .chunks_exact(2)
            .all(|frame| frame[0] == 0)
    );
}

#[test]
fn overlapping_layers_are_attenuated_without_clipping_or_boosting_silence() {
    let mut sound = fixture();
    sound.gain = 4.0;
    sound.layers[0].gain = 4.0;
    let mut second = sound.layers[0].clone();
    second.id = Uuid::now_v7();
    sound.layers.push(second);
    let pcm = samples(&generate(&sound).unwrap());
    let peak = pcm
        .iter()
        .map(|sample| i32::from(*sample).abs())
        .max()
        .unwrap();
    assert_eq!(peak, (32767.0_f64 * 0.95).round() as i32);
    sound.gain = 0.0;
    assert!(samples(&generate(&sound).unwrap()).iter().all(|s| *s == 0));
}

#[test]
fn limits_accept_boundaries_and_reject_excess_and_nonfinite_values() {
    let mut sound = fixture();
    sound.duration = 30.0;
    sound.layers = (0..256)
        .map(|_| {
            let mut layer = fixture().layers.remove(0);
            layer.duration = 0.02;
            layer.release = 0.01;
            layer
        })
        .collect();
    validate(&sound).unwrap();
    sound.layers.push(fixture().layers.remove(0));
    assert_eq!(validate(&sound).unwrap_err().code, ErrorCode::ResourceLimit);
    sound.layers.truncate(256);
    for layer in &mut sound.layers {
        layer.duration = 2.0;
    }
    assert_eq!(validate(&sound).unwrap_err().code, ErrorCode::ResourceLimit);
    for value in [f64::NAN, f64::INFINITY, -1.0, 30.001] {
        let mut sound = fixture();
        sound.duration = value;
        assert!(validate(&sound).is_err());
    }
    let mut sound = fixture();
    sound.layers[0].start = 0.2;
    assert!(validate(&sound).is_err());
    let mut sound = fixture();
    sound.layers[0].attack = 0.0;
    assert!(validate(&sound).is_err());
    let mut sound = fixture();
    sound.layers[0].pan = 0.1;
    assert!(validate(&sound).is_err());
    let mut sound = fixture();
    sound.layers[0].id = sound.id;
    assert!(validate(&sound).is_err());
    let mut sound = fixture();
    sound.layers[0].source = Source::Noise {
        seed: 0,
        low_pass: None,
        high_pass: None,
    };
    assert!(validate(&sound).is_err());
    let mut sound = fixture();
    sound.layers[0].source = Source::Noise {
        seed: 1,
        low_pass: Some(100.0),
        high_pass: Some(200.0),
    };
    assert!(validate(&sound).is_err());
    let mut json = serde_json::to_value(fixture()).unwrap();
    json["file"] = "ignored.wav".into();
    assert!(serde_json::from_value::<Sound>(json).is_err());
}

#[test]
fn cancellation_interrupts_native_work() {
    with_cancellation(Arc::new(AtomicBool::new(true)), || {
        assert_eq!(generate(&fixture()).unwrap_err().code, ErrorCode::Cancelled);
    });
}
