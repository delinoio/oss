// SPDX-License-Identifier: Apache-2.0
use std::{cell::RefCell, ffi::c_void, sync::Arc};

use anyrender_vello_cpu::VelloCpuWindowRenderer;
use blitz_dom::{DocGuard, DocGuardMut, Document, DocumentConfig, EventDriver, EventHandler};
use blitz_html::{HtmlDocument, HtmlProvider};
use blitz_shell::{BlitzApplication, BlitzShellProxy, WindowConfig, create_default_event_loop};
use blitz_traits::{
    events::{BlitzImeEvent, DomEvent, DomEventData, EventState, UiEvent},
    node_id::NodeId,
};
use keyboard_types::{Key, NamedKey};
use winit::{dpi::LogicalSize, window::WindowAttributes};

mod validation;

type Callback = unsafe extern "C" fn(i32, *const u8, usize, *mut c_void);

enum Mutation {
    List(String),
    Count(String),
    Hint(String),
    ClearInput,
}

thread_local! {
    // Native calls made by a compiled handler enqueue updates. Apply them after
    // EventDriver releases its document borrow; do not reenter the DOM mutator.
    static MUTATIONS: RefCell<Vec<Mutation>> = const { RefCell::new(Vec::new()) };
}

unsafe fn borrowed_text<'a>(bytes: *const u8, len: usize) -> &'a str {
    assert!(!bytes.is_null() || len == 0);
    let bytes = if len == 0 {
        &[]
    } else {
        unsafe { std::slice::from_raw_parts(bytes, len) }
    };
    std::str::from_utf8(bytes).expect("ScriptC UTF-8 span")
}

#[derive(Clone, Copy)]
struct Handler {
    callback: Callback,
    context: *mut c_void,
}

impl EventHandler for Handler {
    fn handle_event(
        &mut self,
        chain: &[NodeId],
        event: &mut DomEvent,
        doc: &mut dyn Document,
        state: &mut EventState,
    ) {
        let inner = doc.inner();
        let input = inner
            .query_selector("#new-todo")
            .unwrap()
            .expect("input node");
        let action = match &event.data {
            DomEventData::Input(_) if event.target == input => Some(2),
            DomEventData::Click(_) => chain.iter().find_map(|id| {
                inner
                    .get_node(*id)?
                    .element_data()?
                    .attrs()
                    .iter()
                    .find(|a| &*a.name.local == "data-action")?
                    .value
                    .parse::<i32>()
                    .ok()
            }),
            DomEventData::KeyDown(key) if !key.is_composing && !key.is_auto_repeating => {
                let is_enter = key.key == Key::Named(NamedKey::Enter);
                let is_space = key.key == Key::Character(" ".into());
                if event.target == input && is_enter {
                    Some(1)
                } else if is_enter || is_space {
                    chain.iter().find_map(|id| {
                        inner
                            .get_node(*id)?
                            .element_data()?
                            .attrs()
                            .iter()
                            .find(|a| &*a.name.local == "data-action")?
                            .value
                            .parse::<i32>()
                            .ok()
                    })
                } else {
                    None
                }
            }
            _ => None,
        };
        let Some(action) = action else { return };
        let text = inner
            .get_node(input)
            .and_then(|n| n.element_data())
            .and_then(|e| e.text_input_data())
            .map(|i| i.editor.text().to_string())
            .unwrap_or_default();
        drop(inner);
        state.prevent_default();
        // The call-scoped ScriptC trampoline copies this borrowed UTF-8 span.
        unsafe { (self.callback)(action, text.as_ptr(), text.len(), self.context) };
        tracing::info!(event = "todo_action_dispatched", action);
    }
}

struct TodoDocument {
    html: HtmlDocument,
    handler: Handler,
    composing: bool,
}

impl TodoDocument {
    fn flush(&mut self) {
        let mutations = MUTATIONS.with_borrow_mut(std::mem::take);
        for mutation in mutations {
            match mutation {
                Mutation::List(html) => {
                    let node = self
                        .html
                        .query_selector("#list")
                        .unwrap()
                        .expect("list node");
                    // A changed list replaces its controls. Move focus to the
                    // retained input before removing a possibly focused row.
                    let input = self.html.query_selector("#new-todo").unwrap().unwrap();
                    self.html.set_focus_to(input);
                    self.html.mutate().set_inner_html(node, &html);
                }
                Mutation::Count(text) => {
                    let node = self
                        .html
                        .query_selector("#count")
                        .unwrap()
                        .expect("count node");
                    // Text-only mutation: application data is never parsed
                    // here.
                    let child = self.html.get_node(node).unwrap().children[0];
                    self.html.mutate().set_node_text(child, &text);
                }
                Mutation::ClearInput => {
                    let node = self.html.query_selector("#new-todo").unwrap().unwrap();
                    self.html.with_text_input(node, |mut driver| {
                        driver.select_all();
                        driver.delete_selection();
                    });
                    self.html.set_focus_to(node);
                }
                Mutation::Hint(text) => {
                    let node = self
                        .html
                        .query_selector("#input-hint")
                        .unwrap()
                        .expect("hint node");
                    let child = self.html.get_node(node).unwrap().children[0];
                    self.html.mutate().set_node_text(child, &text);
                }
            }
        }
    }
}

impl Document for TodoDocument {
    fn inner(&self) -> DocGuard<'_> {
        self.html.inner()
    }

    fn inner_mut(&mut self) -> DocGuardMut<'_> {
        self.html.inner_mut()
    }

    fn handle_ui_event(&mut self, mut event: UiEvent) {
        // The pinned shell sets every key's is_composing to false. Track IME
        // preedit here until upstream forwards composition state itself, so
        // Enter used to confirm composition cannot also submit a task.
        match &mut event {
            UiEvent::Ime(BlitzImeEvent::Preedit(text, _)) => self.composing = !text.is_empty(),
            UiEvent::Ime(BlitzImeEvent::Commit(_) | BlitzImeEvent::Disabled) => {
                self.composing = false
            }
            UiEvent::KeyDown(key) | UiEvent::KeyUp(key) => key.is_composing |= self.composing,
            _ => {}
        }
        EventDriver::new(&mut self.html, self.handler).handle_ui_event(event);
        self.flush();
    }
}

/// Queue escaped list HTML or plain remaining-count text from compiled TSX.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn tn_todo_update(target: i32, bytes: *const u8, len: usize) {
    let value = unsafe { borrowed_text(bytes, len) }.to_owned();
    MUTATIONS.with_borrow_mut(|queue| {
        queue.push(match target {
            1 => Mutation::List(value),
            2 => Mutation::Count(value),
            3 => Mutation::Hint(value),
            _ => panic!("unsupported todo update target"),
        })
    });
}

#[unsafe(no_mangle)]
pub extern "C" fn tn_todo_clear_input() {
    MUTATIONS.with_borrow_mut(|queue| queue.push(Mutation::ClearInput));
}

/// The callback is valid for this call only. The event loop and every callback
/// execute on the calling main thread until the user closes the window.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn tn_todo_run(
    bytes: *const u8,
    len: usize,
    callback: Callback,
    context: *mut c_void,
) {
    assert!(cfg!(all(target_os = "macos", target_arch = "aarch64")));
    tracing_subscriber::fmt()
        .with_target(false)
        .with_writer(std::io::stderr)
        .with_ansi(std::env::var_os("NO_COLOR").is_none())
        .init();
    let source = unsafe { borrowed_text(bytes, len) };
    let mut html = HtmlDocument::from_html(
        source,
        DocumentConfig {
            html_parser_provider: Some(Arc::new(HtmlProvider)),
            ..Default::default()
        },
    );
    html.resolve(0.0);
    let input = html
        .query_selector("#new-todo")
        .unwrap()
        .expect("input node");
    html.set_focus_to(input);
    let mut doc = TodoDocument {
        html,
        handler: Handler { callback, context },
        composing: false,
    };
    if std::env::args().nth(1).as_deref() == Some("--self-test") {
        let output = std::env::args()
            .nth(2)
            .expect("explicit self-test output directory");
        validation::run(&mut doc, std::path::Path::new(&output));
        println!("typenative-todo-self-test:passed");
        return;
    }
    let event_loop = create_default_event_loop();
    let (proxy, queue) = BlitzShellProxy::new(event_loop.create_proxy());
    let mut app = BlitzApplication::new(proxy, queue);
    app.add_window(WindowConfig::with_attributes(
        Box::new(doc),
        VelloCpuWindowRenderer::new(),
        WindowAttributes::default()
            .with_title("TypeNative Todo")
            .with_surface_size(LogicalSize::new(640.0, 560.0))
            .with_min_surface_size(LogicalSize::new(360.0, 320.0)),
    ));
    tracing::info!(event = "todo_window_starting", target = "macos-arm64");
    event_loop.run_app(app).expect("native event loop");
    tracing::info!(event = "todo_window_closed");
}
