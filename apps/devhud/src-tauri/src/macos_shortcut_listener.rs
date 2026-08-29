//! DevHud-owned, physical-code-only macOS shortcut listener.
//!
//! The production adapter installs a passive CoreGraphics event tap and
//! returns every native event unchanged. It deliberately avoids all keyboard
//! layout and text-generation APIs. Only normalized keys from the closed
//! shortcut contract leave this module.

use crate::shortcuts::{
    NativeKey, NativeKeyEvent, ShortcutFailure, ShortcutPlatform, normalize_native_key,
};

const COMMAND_FLAG: u64 = 1 << 20;
const CONTROL_FLAG: u64 = 1 << 18;
const SHIFT_FLAG: u64 = 1 << 17;
const OPTION_FLAG: u64 = 1 << 19;

const MACOS_MODIFIERS: [(u16, NativeKey); 8] = [
    (54, NativeKey::RightPrimary),
    (55, NativeKey::LeftMeta),
    (59, NativeKey::LeftControl),
    (62, NativeKey::OtherPrimary),
    (56, NativeKey::LeftShift),
    (60, NativeKey::RightShift),
    (58, NativeKey::LeftAlt),
    (61, NativeKey::RightAlt),
];

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum MacosEventKind {
    KeyDown,
    KeyUp,
    FlagsChanged { pressed: bool },
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
struct MacosEventRecord {
    kind: MacosEventKind,
    physical_code: u32,
    flags: u64,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum ModifierFamily {
    Command,
    Control,
    Shift,
    Option,
}

#[derive(Default)]
struct MacosShortcutAdapter {
    right_command: bool,
    left_command: bool,
    left_control: bool,
    right_control: bool,
    left_shift: bool,
    right_shift: bool,
    left_option: bool,
    right_option: bool,
}

impl MacosShortcutAdapter {
    fn process(&mut self, record: MacosEventRecord) -> [Option<NativeKeyEvent>; 2] {
        let Some(key) = normalize_native_key(ShortcutPlatform::Macos, record.physical_code) else {
            return [None, None];
        };
        self.process_recognized(record.kind, key, record.flags)
    }

    fn process_recognized(
        &mut self,
        kind: MacosEventKind,
        key: NativeKey,
        flags: u64,
    ) -> [Option<NativeKeyEvent>; 2] {
        match kind {
            MacosEventKind::KeyDown | MacosEventKind::KeyUp => {
                if !matches!(key, NativeKey::Key(_)) {
                    return [None, None];
                }
                [
                    Some(NativeKeyEvent {
                        key,
                        pressed: kind == MacosEventKind::KeyDown,
                    }),
                    None,
                ]
            }
            MacosEventKind::FlagsChanged { pressed } => self.process_modifier(key, flags, pressed),
        }
    }

    fn reconcile_modifiers(
        &mut self,
        current: [(NativeKey, bool); 8],
    ) -> [Option<NativeKeyEvent>; 8] {
        let mut events = [None; 8];
        let mut next = 0;
        for (key, pressed) in current {
            let state = self.modifier_state_mut(key);
            if *state == pressed {
                continue;
            }
            *state = pressed;
            events[next] = Some(NativeKeyEvent { key, pressed });
            next += 1;
        }
        events
    }

    fn process_modifier(
        &mut self,
        key: NativeKey,
        flags: u64,
        pressed: bool,
    ) -> [Option<NativeKeyEvent>; 2] {
        let Some(family) = modifier_family(key) else {
            return [None, None];
        };
        if flags & family_flag(family) == 0 {
            let mut events = [None, None];
            let mut next = 0;
            for member in family_members(family) {
                let state = self.modifier_state_mut(member);
                if *state {
                    *state = false;
                    events[next] = Some(NativeKeyEvent {
                        key: member,
                        pressed: false,
                    });
                    next += 1;
                }
            }
            return events;
        }

        let state = self.modifier_state_mut(key);
        *state = pressed;
        [Some(NativeKeyEvent { key, pressed }), None]
    }

    fn modifier_state_mut(&mut self, key: NativeKey) -> &mut bool {
        match key {
            NativeKey::RightPrimary => &mut self.right_command,
            NativeKey::LeftMeta => &mut self.left_command,
            NativeKey::LeftControl => &mut self.left_control,
            NativeKey::OtherPrimary => &mut self.right_control,
            NativeKey::LeftShift => &mut self.left_shift,
            NativeKey::RightShift => &mut self.right_shift,
            NativeKey::LeftAlt => &mut self.left_option,
            NativeKey::RightAlt => &mut self.right_option,
            NativeKey::Key(_) => unreachable!("ordinary keys are not modifier state"),
        }
    }
}

fn modifier_family(key: NativeKey) -> Option<ModifierFamily> {
    match key {
        NativeKey::RightPrimary | NativeKey::LeftMeta => Some(ModifierFamily::Command),
        NativeKey::LeftControl | NativeKey::OtherPrimary => Some(ModifierFamily::Control),
        NativeKey::LeftShift | NativeKey::RightShift => Some(ModifierFamily::Shift),
        NativeKey::LeftAlt | NativeKey::RightAlt => Some(ModifierFamily::Option),
        NativeKey::Key(_) => None,
    }
}

const fn family_flag(family: ModifierFamily) -> u64 {
    match family {
        ModifierFamily::Command => COMMAND_FLAG,
        ModifierFamily::Control => CONTROL_FLAG,
        ModifierFamily::Shift => SHIFT_FLAG,
        ModifierFamily::Option => OPTION_FLAG,
    }
}

const fn family_members(family: ModifierFamily) -> [NativeKey; 2] {
    match family {
        ModifierFamily::Command => [NativeKey::RightPrimary, NativeKey::LeftMeta],
        ModifierFamily::Control => [NativeKey::LeftControl, NativeKey::OtherPrimary],
        ModifierFamily::Shift => [NativeKey::LeftShift, NativeKey::RightShift],
        ModifierFamily::Option => [NativeKey::LeftAlt, NativeKey::RightAlt],
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum EventTapFailure {
    Permission,
    Creation,
    Disabled,
    RunLoop,
}

impl EventTapFailure {
    const fn stable_failure(self) -> ShortcutFailure {
        match self {
            Self::Permission => ShortcutFailure::PermissionDenied,
            Self::Creation | Self::Disabled | Self::RunLoop => ShortcutFailure::RegistrationFailed,
        }
    }
}

#[cfg(target_os = "macos")]
mod native {
    use std::ffi::c_void;

    use super::{
        EventTapFailure, MACOS_MODIFIERS, MacosEventKind, MacosShortcutAdapter, ShortcutFailure,
    };
    use crate::shortcuts::{
        NativeKey, NativeKeyEvent, ShortcutPermission, ShortcutPlatform, macos_shortcut_permission,
        normalize_native_key,
    };

    type CGEventRef = *mut c_void;
    type CFMachPortRef = *mut c_void;
    type CFRunLoopRef = *mut c_void;
    type CFRunLoopSourceRef = *mut c_void;

    const CG_SESSION_EVENT_TAP: u32 = 1;
    const CG_HEAD_INSERT_EVENT_TAP: u32 = 0;
    const CG_EVENT_TAP_OPTION_LISTEN_ONLY: u32 = 1;
    const CG_EVENT_KEY_DOWN: u32 = 10;
    const CG_EVENT_KEY_UP: u32 = 11;
    const CG_EVENT_FLAGS_CHANGED: u32 = 12;
    const CG_EVENT_TAP_DISABLED_BY_TIMEOUT: u32 = u32::MAX - 1;
    const CG_EVENT_TAP_DISABLED_BY_USER_INPUT: u32 = u32::MAX;
    const CG_KEYBOARD_EVENT_KEYCODE: u32 = 9;
    const CG_EVENT_SOURCE_STATE_HID_SYSTEM_STATE: i32 = 1;

    type CGEventTapCallback = unsafe extern "C" fn(
        proxy: *mut c_void,
        event_type: u32,
        event: CGEventRef,
        user_info: *mut c_void,
    ) -> CGEventRef;

    #[link(name = "CoreGraphics", kind = "framework")]
    unsafe extern "C" {
        fn CGEventTapCreate(
            tap: u32,
            place: u32,
            options: u32,
            events_of_interest: u64,
            callback: Option<CGEventTapCallback>,
            user_info: *mut c_void,
        ) -> CFMachPortRef;
        fn CGEventGetIntegerValueField(event: CGEventRef, field: u32) -> i64;
        fn CGEventGetFlags(event: CGEventRef) -> u64;
        fn CGEventSourceKeyState(state_id: i32, key: u16) -> bool;
        fn CGEventTapIsEnabled(tap: CFMachPortRef) -> bool;
    }

    #[link(name = "CoreFoundation", kind = "framework")]
    unsafe extern "C" {
        fn CFMachPortCreateRunLoopSource(
            allocator: *const c_void,
            port: CFMachPortRef,
            order: isize,
        ) -> CFRunLoopSourceRef;
        fn CFMachPortInvalidate(port: CFMachPortRef);
        fn CFRunLoopGetCurrent() -> CFRunLoopRef;
        fn CFRunLoopAddSource(
            run_loop: CFRunLoopRef,
            source: CFRunLoopSourceRef,
            mode: *const c_void,
        );
        fn CFRunLoopRemoveSource(
            run_loop: CFRunLoopRef,
            source: CFRunLoopSourceRef,
            mode: *const c_void,
        );
        fn CFRunLoopRun();
        fn CFRunLoopStop(run_loop: CFRunLoopRef);
        fn CFRelease(value: *const c_void);
        static kCFRunLoopCommonModes: *const c_void;
    }

    struct EventTapContext<F> {
        adapter: MacosShortcutAdapter,
        dispatch: F,
        run_loop: CFRunLoopRef,
        failure: Option<EventTapFailure>,
    }

    unsafe fn current_modifier_states() -> [(NativeKey, bool); 8] {
        MACOS_MODIFIERS.map(|(physical_code, key)| {
            (key, unsafe {
                CGEventSourceKeyState(CG_EVENT_SOURCE_STATE_HID_SYSTEM_STATE, physical_code)
            })
        })
    }

    unsafe extern "C" fn event_tap_callback<F>(
        _proxy: *mut c_void,
        event_type: u32,
        event: CGEventRef,
        user_info: *mut c_void,
    ) -> CGEventRef
    where
        F: FnMut(NativeKeyEvent),
    {
        // CoreGraphics owns `event`. Listen-only taps must return the exact
        // reference they received and must never mutate or replace it.
        let context = unsafe { &mut *(user_info.cast::<EventTapContext<F>>()) };
        if matches!(
            event_type,
            CG_EVENT_TAP_DISABLED_BY_TIMEOUT | CG_EVENT_TAP_DISABLED_BY_USER_INPUT
        ) {
            context.failure = Some(EventTapFailure::Disabled);
            unsafe { CFRunLoopStop(context.run_loop) };
            return event;
        }

        let flags_changed = match event_type {
            CG_EVENT_KEY_DOWN | CG_EVENT_KEY_UP => false,
            CG_EVENT_FLAGS_CHANGED => true,
            _ => return event,
        };
        let physical_code =
            unsafe { CGEventGetIntegerValueField(event, CG_KEYBOARD_EVENT_KEYCODE) };
        let Ok(physical_code) = u32::try_from(physical_code) else {
            return event;
        };
        let Some(key) = normalize_native_key(ShortcutPlatform::Macos, physical_code) else {
            return event;
        };
        if !flags_changed && matches!(key, NativeKey::Key(_)) {
            // A replacement tap receives no press event for a modifier that
            // predates its installation, so refresh every supported side
            // before ordinary input reaches the matcher.
            for normalized in context
                .adapter
                .reconcile_modifiers(unsafe { current_modifier_states() })
                .into_iter()
                .flatten()
            {
                (context.dispatch)(normalized);
            }
        }
        let (kind, flags) = if flags_changed {
            let Ok(key_code) = u16::try_from(physical_code) else {
                return event;
            };
            (
                MacosEventKind::FlagsChanged {
                    pressed: unsafe {
                        CGEventSourceKeyState(CG_EVENT_SOURCE_STATE_HID_SYSTEM_STATE, key_code)
                    },
                },
                unsafe { CGEventGetFlags(event) },
            )
        } else {
            (
                match event_type {
                    CG_EVENT_KEY_DOWN => MacosEventKind::KeyDown,
                    CG_EVENT_KEY_UP => MacosEventKind::KeyUp,
                    _ => unreachable!("event type was filtered before normalization"),
                },
                0,
            )
        };
        for normalized in context
            .adapter
            .process_recognized(kind, key, flags)
            .into_iter()
            .flatten()
        {
            (context.dispatch)(normalized);
        }
        event
    }

    pub(crate) fn listen<F>(dispatch: F) -> ShortcutFailure
    where
        F: FnMut(NativeKeyEvent),
    {
        if macos_shortcut_permission(false) != ShortcutPermission::Available {
            return EventTapFailure::Permission.stable_failure();
        }

        let run_loop = unsafe { CFRunLoopGetCurrent() };
        if run_loop.is_null() {
            return EventTapFailure::Creation.stable_failure();
        }
        let mut context = Box::new(EventTapContext {
            adapter: MacosShortcutAdapter::default(),
            dispatch,
            run_loop,
            failure: None,
        });
        let mask = (1_u64 << CG_EVENT_KEY_DOWN)
            | (1_u64 << CG_EVENT_KEY_UP)
            | (1_u64 << CG_EVENT_FLAGS_CHANGED);
        let tap = unsafe {
            CGEventTapCreate(
                CG_SESSION_EVENT_TAP,
                CG_HEAD_INSERT_EVENT_TAP,
                CG_EVENT_TAP_OPTION_LISTEN_ONLY,
                mask,
                Some(event_tap_callback::<F>),
                (&mut *context as *mut EventTapContext<F>).cast(),
            )
        };
        if tap.is_null() {
            return EventTapFailure::Creation.stable_failure();
        }
        let source = unsafe { CFMachPortCreateRunLoopSource(std::ptr::null(), tap, 0) };
        if source.is_null() {
            unsafe {
                CFMachPortInvalidate(tap);
                CFRelease(tap);
            }
            return EventTapFailure::Creation.stable_failure();
        }

        unsafe {
            CFRunLoopAddSource(run_loop, source, kCFRunLoopCommonModes);
        }
        if unsafe { !CGEventTapIsEnabled(tap) } {
            context.failure = Some(EventTapFailure::Creation);
        } else {
            unsafe { CFRunLoopRun() };
        }
        unsafe {
            CFRunLoopRemoveSource(run_loop, source, kCFRunLoopCommonModes);
            CFMachPortInvalidate(tap);
            CFRelease(source);
            CFRelease(tap);
        }
        context
            .failure
            .unwrap_or(EventTapFailure::RunLoop)
            .stable_failure()
    }
}

#[cfg(target_os = "macos")]
pub(crate) use native::listen;

#[cfg(test)]
mod tests {
    use super::*;
    use crate::shortcuts::{
        NativeShortcutBackend, ShortcutAction, ShortcutBindings, ShortcutPermission,
        ShortcutService,
    };

    #[derive(Default)]
    struct FakeBackend;

    impl NativeShortcutBackend for FakeBackend {
        fn install(&mut self, _: &ShortcutBindings) -> Result<(), ShortcutFailure> {
            Ok(())
        }

        fn permission(&self) -> ShortcutPermission {
            ShortcutPermission::Available
        }

        fn platform(&self) -> ShortcutPlatform {
            ShortcutPlatform::Macos
        }
    }

    fn record(kind: MacosEventKind, physical_code: u32, flags: u64) -> MacosEventRecord {
        MacosEventRecord {
            kind,
            physical_code,
            flags,
        }
    }

    fn modifier_record(physical_code: u32, flags: u64, pressed: bool) -> MacosEventRecord {
        record(
            MacosEventKind::FlagsChanged { pressed },
            physical_code,
            flags,
        )
    }

    fn modifier_states(held: &[u16]) -> [(NativeKey, bool); 8] {
        MACOS_MODIFIERS.map(|(physical_code, key)| (key, held.contains(&physical_code)))
    }

    fn deliver(
        adapter: &mut MacosShortcutAdapter,
        matcher: &mut ShortcutService<FakeBackend>,
        record: MacosEventRecord,
    ) -> Vec<ShortcutAction> {
        adapter
            .process(record)
            .into_iter()
            .flatten()
            .filter_map(|event| matcher.process(event))
            .collect()
    }

    fn deliver_reconciled(
        adapter: &mut MacosShortcutAdapter,
        matcher: &mut ShortcutService<FakeBackend>,
        held_modifiers: &[u16],
        record: MacosEventRecord,
    ) -> Vec<ShortcutAction> {
        adapter
            .reconcile_modifiers(modifier_states(held_modifiers))
            .into_iter()
            .flatten()
            .for_each(|event| {
                assert!(matcher.process(event).is_none());
            });
        deliver(adapter, matcher, record)
    }

    #[test]
    fn maps_only_contracted_ordinary_physical_keys() {
        let mut adapter = MacosShortcutAdapter::default();
        assert_eq!(
            adapter.process(record(MacosEventKind::KeyDown, 40, 0)),
            [
                Some(NativeKeyEvent {
                    key: NativeKey::Key(crate::shortcuts::ShortcutKey::KeyK),
                    pressed: true,
                }),
                None,
            ]
        );
        assert!(
            !adapter.process(record(MacosEventKind::KeyUp, 40, 0))[0]
                .expect("mapped release")
                .pressed
        );
        assert_eq!(
            adapter.process(record(MacosEventKind::KeyDown, 0, 0)),
            [None, None]
        );
        assert_eq!(
            adapter.process(record(MacosEventKind::KeyUp, u32::MAX, 0)),
            [None, None]
        );
    }

    #[test]
    fn right_command_plus_k_emits_once_per_press_release_cycle() {
        let mut adapter = MacosShortcutAdapter::default();
        let mut matcher = ShortcutService::new(FakeBackend);
        assert!(
            deliver(
                &mut adapter,
                &mut matcher,
                modifier_record(54, COMMAND_FLAG, true)
            )
            .is_empty()
        );
        assert_eq!(
            deliver(
                &mut adapter,
                &mut matcher,
                record(MacosEventKind::KeyDown, 40, 0)
            ),
            vec![ShortcutAction::ShellCommandPalette]
        );
        assert!(
            deliver(
                &mut adapter,
                &mut matcher,
                record(MacosEventKind::KeyDown, 40, 0)
            )
            .is_empty()
        );
        assert!(
            deliver(
                &mut adapter,
                &mut matcher,
                record(MacosEventKind::KeyUp, 40, 0)
            )
            .is_empty()
        );
        assert_eq!(
            deliver(
                &mut adapter,
                &mut matcher,
                record(MacosEventKind::KeyDown, 40, 0)
            ),
            vec![ShortcutAction::ShellCommandPalette]
        );
    }

    #[test]
    fn left_command_and_right_control_suppress_right_command() {
        for (extra_code, extra_flag) in [(55, COMMAND_FLAG), (62, CONTROL_FLAG), (59, CONTROL_FLAG)]
        {
            let mut adapter = MacosShortcutAdapter::default();
            let mut matcher = ShortcutService::new(FakeBackend);
            for input in [
                modifier_record(54, COMMAND_FLAG, true),
                modifier_record(extra_code, COMMAND_FLAG | extra_flag, true),
            ] {
                assert!(deliver(&mut adapter, &mut matcher, input).is_empty());
            }
            assert!(
                deliver(
                    &mut adapter,
                    &mut matcher,
                    record(MacosEventKind::KeyDown, 40, 0)
                )
                .is_empty()
            );
        }
    }

    #[test]
    fn both_command_sides_remain_distinct_until_each_release() {
        let mut adapter = MacosShortcutAdapter::default();
        let mut matcher = ShortcutService::new(FakeBackend);
        for input in [
            modifier_record(54, COMMAND_FLAG, true),
            modifier_record(55, COMMAND_FLAG, true),
        ] {
            assert!(deliver(&mut adapter, &mut matcher, input).is_empty());
        }
        assert!(
            deliver(
                &mut adapter,
                &mut matcher,
                record(MacosEventKind::KeyDown, 40, 0)
            )
            .is_empty()
        );
        assert!(
            deliver(
                &mut adapter,
                &mut matcher,
                record(MacosEventKind::KeyUp, 40, 0)
            )
            .is_empty()
        );
        assert!(
            deliver(
                &mut adapter,
                &mut matcher,
                modifier_record(55, COMMAND_FLAG, false)
            )
            .is_empty()
        );
        assert_eq!(
            deliver(
                &mut adapter,
                &mut matcher,
                record(MacosEventKind::KeyDown, 40, 0)
            ),
            vec![ShortcutAction::ShellCommandPalette]
        );

        let mut adapter = MacosShortcutAdapter::default();
        let mut matcher = ShortcutService::new(FakeBackend);
        for input in [
            modifier_record(54, COMMAND_FLAG, true),
            modifier_record(55, COMMAND_FLAG, true),
            modifier_record(54, COMMAND_FLAG, false),
        ] {
            assert!(deliver(&mut adapter, &mut matcher, input).is_empty());
        }
        assert!(
            deliver(
                &mut adapter,
                &mut matcher,
                record(MacosEventKind::KeyDown, 40, 0)
            )
            .is_empty()
        );
    }

    #[test]
    fn releasing_right_control_restores_the_exact_right_command_chord() {
        let mut adapter = MacosShortcutAdapter::default();
        let mut matcher = ShortcutService::new(FakeBackend);
        for input in [
            modifier_record(54, COMMAND_FLAG, true),
            modifier_record(62, COMMAND_FLAG | CONTROL_FLAG, true),
        ] {
            assert!(deliver(&mut adapter, &mut matcher, input).is_empty());
        }
        assert!(
            deliver(
                &mut adapter,
                &mut matcher,
                record(MacosEventKind::KeyDown, 40, 0)
            )
            .is_empty()
        );
        assert!(
            deliver(
                &mut adapter,
                &mut matcher,
                record(MacosEventKind::KeyUp, 40, 0)
            )
            .is_empty()
        );
        assert!(
            deliver(
                &mut adapter,
                &mut matcher,
                modifier_record(62, COMMAND_FLAG, false)
            )
            .is_empty()
        );
        assert_eq!(
            deliver(
                &mut adapter,
                &mut matcher,
                record(MacosEventKind::KeyDown, 40, 0)
            ),
            vec![ShortcutAction::ShellCommandPalette]
        );
    }

    #[test]
    fn a_cleared_family_flag_releases_every_tracked_side() {
        for (left, right, flag, expected) in [
            (
                55,
                54,
                COMMAND_FLAG,
                [NativeKey::RightPrimary, NativeKey::LeftMeta],
            ),
            (
                59,
                62,
                CONTROL_FLAG,
                [NativeKey::LeftControl, NativeKey::OtherPrimary],
            ),
            (
                56,
                60,
                SHIFT_FLAG,
                [NativeKey::LeftShift, NativeKey::RightShift],
            ),
            (
                58,
                61,
                OPTION_FLAG,
                [NativeKey::LeftAlt, NativeKey::RightAlt],
            ),
        ] {
            let mut adapter = MacosShortcutAdapter::default();
            assert!(
                adapter.process(modifier_record(left, flag, true))[0]
                    .expect("left press")
                    .pressed
            );
            assert!(
                adapter.process(modifier_record(right, flag, true))[0]
                    .expect("right press")
                    .pressed
            );
            let released = adapter
                .process(modifier_record(right, 0, false))
                .into_iter()
                .flatten()
                .collect::<Vec<_>>();
            assert_eq!(released.len(), 2);
            assert!(released.iter().all(|event| !event.pressed));
            for key in expected {
                assert!(released.iter().any(|event| event.key == key));
            }
        }
    }

    #[test]
    fn listener_replacement_reconciles_held_modifiers_before_input() {
        let mut adapter = MacosShortcutAdapter::default();
        let mut matcher = ShortcutService::new(FakeBackend);
        assert!(
            deliver(
                &mut adapter,
                &mut matcher,
                modifier_record(54, COMMAND_FLAG, true)
            )
            .is_empty()
        );
        adapter = MacosShortcutAdapter::default();
        matcher.clear_pressed_keys();
        assert_eq!(
            deliver_reconciled(
                &mut adapter,
                &mut matcher,
                &[54],
                record(MacosEventKind::KeyDown, 40, 0),
            ),
            vec![ShortcutAction::ShellCommandPalette]
        );
        assert!(
            deliver_reconciled(
                &mut adapter,
                &mut matcher,
                &[54],
                record(MacosEventKind::KeyDown, 18, 0),
            )
            .is_empty()
        );
        assert_eq!(
            deliver_reconciled(
                &mut adapter,
                &mut matcher,
                &[],
                record(MacosEventKind::KeyDown, 19, 0),
            ),
            vec![ShortcutAction::RealqaCaptureActiveWindow]
        );
    }

    #[test]
    fn reconciliation_tracks_every_supported_modifier_side() {
        for (physical_code, expected_key) in MACOS_MODIFIERS {
            let mut adapter = MacosShortcutAdapter::default();
            assert_eq!(
                adapter
                    .reconcile_modifiers(modifier_states(&[physical_code]))
                    .into_iter()
                    .flatten()
                    .collect::<Vec<_>>(),
                vec![NativeKeyEvent {
                    key: expected_key,
                    pressed: true,
                }]
            );
            assert_eq!(
                adapter
                    .reconcile_modifiers(modifier_states(&[]))
                    .into_iter()
                    .flatten()
                    .collect::<Vec<_>>(),
                vec![NativeKeyEvent {
                    key: expected_key,
                    pressed: false,
                }]
            );
        }
    }

    #[test]
    fn retry_does_not_invert_a_released_modifier_while_its_family_remains_held() {
        let mut adapter = MacosShortcutAdapter::default();
        let mut matcher = ShortcutService::new(FakeBackend);
        for input in [
            modifier_record(54, COMMAND_FLAG, true),
            modifier_record(55, COMMAND_FLAG, true),
        ] {
            assert!(deliver(&mut adapter, &mut matcher, input).is_empty());
        }

        adapter = MacosShortcutAdapter::default();
        matcher.clear_pressed_keys();
        assert!(
            deliver(
                &mut adapter,
                &mut matcher,
                modifier_record(54, COMMAND_FLAG, false)
            )
            .is_empty()
        );
        assert!(
            deliver(
                &mut adapter,
                &mut matcher,
                record(MacosEventKind::KeyDown, 40, 0)
            )
            .is_empty()
        );
    }

    #[test]
    fn listener_failures_use_only_existing_stable_classifications() {
        assert_eq!(
            EventTapFailure::Permission.stable_failure(),
            ShortcutFailure::PermissionDenied
        );
        for failure in [
            EventTapFailure::Creation,
            EventTapFailure::Disabled,
            EventTapFailure::RunLoop,
        ] {
            assert_eq!(
                failure.stable_failure(),
                ShortcutFailure::RegistrationFailed
            );
        }
    }
}
