//! Pure Figma revision planning. Network, credentials and canvas execution
//! belong to the caller. Plans never infer ownership from names or React keys.
use std::collections::{BTreeMap, BTreeSet};

use serde::{Deserialize, Serialize};
use serde_json::Value;

pub const MAX_MODEL_BYTES: usize = 16 * 1024 * 1024;
pub const MAX_NODES: usize = 20_000;
pub const MAX_DEPTH: usize = 48;
pub const MAX_CODE_UNITS: usize = 50_000;

#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum Kind {
    Page,
    Frame,
    Text,
    Rectangle,
    Ellipse,
    Line,
    Vector,
    Component,
    ComponentSet,
    Instance,
    Collection,
    Variable,
    PaintStyle,
    TextStyle,
}
impl Kind {
    pub fn resource(self) -> bool {
        matches!(
            self,
            Self::Collection | Self::Variable | Self::PaintStyle | Self::TextStyle
        )
    }

    fn container(self) -> bool {
        matches!(
            self,
            Self::Page | Self::Frame | Self::Component | Self::ComponentSet
        )
    }
}

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Entity {
    pub key: String,
    pub kind: Kind,
    /// Another Forge key or @ followed by an explicitly selected remote ID.
    pub parent: Option<String>,
    pub page: Option<String>,
    #[serde(default)]
    pub props: BTreeMap<String, Value>,
    /// Only these explicit managed children may be reordered or removed.
    #[serde(default)]
    pub children: Vec<String>,
}

#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum Action {
    Create,
    Update,
    Delete,
}
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Operation {
    pub action: Action,
    pub entity: Entity,
    pub previous: Option<Entity>,
}
#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Input {
    pub desired: Vec<Entity>,
    pub previous: Vec<Entity>,
    pub bindings: BTreeMap<String, String>,
    pub payload_limit: usize,
}
#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Batch {
    pub page: Option<String>,
    pub operations: Vec<Operation>,
}
#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Plan {
    pub batches: Vec<Batch>,
    pub operation_count: usize,
}
#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum Error {
    MalformedInput,
    ResourceLimit,
    InvalidTarget,
    UnsupportedEdit,
    Conflict,
}

type Result<T> = std::result::Result<T, Error>;
fn remote_id(id: &str) -> bool {
    !id.is_empty()
        && id.len() <= 256
        && id
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b":;-_,.".contains(&b))
}
fn reference(id: &str) -> bool {
    id.strip_prefix('@').map(remote_id).unwrap_or_else(|| {
        uuid::Uuid::parse_str(id).is_ok_and(|v| v.get_version_num() == 7 && v.to_string() == id)
    })
}
fn number(value: &Value, low: f64, high: f64) -> bool {
    value
        .as_f64()
        .is_some_and(|v| v.is_finite() && (low..=high).contains(&v))
}
fn string(value: &Value, max: usize) -> bool {
    value
        .as_str()
        .is_some_and(|v| v.len() <= max && !v.contains('\0'))
}
fn one_of(value: &Value, allowed: &[&str]) -> bool {
    value.as_str().is_some_and(|v| allowed.contains(&v))
}
fn color(value: &Value) -> bool {
    value.as_object().is_some_and(|v| {
        v.len() == 3
            && ["r", "g", "b"]
                .iter()
                .all(|k| v.get(*k).is_some_and(|n| number(n, 0., 1.)))
    })
}
fn paints(value: &Value) -> bool {
    value.as_array().is_some_and(|paints| {
        paints.len() <= 16
            && paints.iter().all(|p| {
                p.as_object().is_some_and(|o| {
                    o.keys().all(|k| {
                        [
                            "type",
                            "color",
                            "opacity",
                            "visible",
                            "imageHash",
                            "scaleMode",
                            "gradientStops",
                            "gradientTransform",
                        ]
                        .contains(&k.as_str())
                    })
                }) && (p["type"] == "SOLID" && color(&p["color"])
                    || p["type"] == "IMAGE"
                        && string(&p["imageHash"], 256)
                        && one_of(&p["scaleMode"], &["FILL", "FIT", "CROP", "TILE"]))
                    && p.get("opacity").is_none_or(|v| number(v, 0., 1.))
            })
    })
}
fn validate_props(e: &Entity) -> Result<()> {
    for (key, value) in &e.props {
        let valid = match key.as_str() {
            "name" => string(value, 1024),
            "characters" => e.kind == Kind::Text && string(value, 65536),
            "x" | "y" => !e.kind.resource() && number(value, -1_000_000., 1_000_000.),
            "width" | "height" => !e.kind.resource() && number(value, 0.01, 100_000.),
            "fontSize" => {
                matches!(e.kind, Kind::Text | Kind::TextStyle) && number(value, 1., 1000.)
            }
            "cornerRadius" | "strokeWeight" | "paddingTop" | "paddingBottom" | "paddingLeft"
            | "paddingRight" | "itemSpacing" => number(value, 0., 10000.),
            "opacity" => number(value, 0., 1.),
            "rotation" => number(value, -360., 360.),
            "visible" | "clipsContent" => value.is_boolean(),
            "fills" | "strokes" => paints(value),
            "layoutMode" => {
                e.kind.container() && one_of(value, &["NONE", "HORIZONTAL", "VERTICAL"])
            }
            "primaryAxisAlignItems" => one_of(value, &["MIN", "CENTER", "MAX", "SPACE_BETWEEN"]),
            "counterAxisAlignItems" => one_of(value, &["MIN", "CENTER", "MAX", "BASELINE"]),
            "primaryAxisSizingMode" | "counterAxisSizingMode" => one_of(value, &["AUTO", "FIXED"]),
            "layoutSizingHorizontal" | "layoutSizingVertical" => {
                one_of(value, &["FIXED", "HUG", "FILL"])
            }
            "textAutoResize" => {
                e.kind == Kind::Text
                    && one_of(value, &["NONE", "HEIGHT", "WIDTH_AND_HEIGHT", "TRUNCATE"])
            }
            "textAlignHorizontal" => one_of(value, &["LEFT", "CENTER", "RIGHT", "JUSTIFIED"]),
            "fontName" => {
                matches!(e.kind, Kind::Text | Kind::TextStyle)
                    && value.as_object().is_some_and(|o| {
                        o.len() == 2
                            && string(&value["family"], 256)
                            && string(&value["style"], 256)
                    })
            }
            "lineHeight" | "letterSpacing" => {
                one_of(&value["unit"], &["PIXELS", "PERCENT", "AUTO"])
                    && (value["unit"] == "AUTO" || number(&value["value"], -1000., 10000.))
            }
            "vectorPaths" => {
                e.kind == Kind::Vector
                    && value.as_array().is_some_and(|v| {
                        v.len() <= 128
                            && v.iter().all(|p| {
                                string(&p["data"], 16000)
                                    && one_of(&p["windingRule"], &["NONZERO", "EVENODD"])
                            })
                    })
            }
            "component" => e.kind == Kind::Instance && value.as_str().is_some_and(reference),
            "componentProperties" => {
                e.kind == Kind::Instance
                    && value.as_object().is_some_and(|v| {
                        v.len() <= 100 && v.values().all(|v| v.is_boolean() || string(v, 2048))
                    })
            }
            "variants" => {
                e.kind == Kind::ComponentSet
                    && value.as_array().is_some_and(|v| {
                        !v.is_empty()
                            && v.len() <= 100
                            && v.iter().all(|v| v.as_str().is_some_and(reference))
                    })
            }
            "collection" => e.kind == Kind::Variable && value.as_str().is_some_and(reference),
            "resolvedType" => {
                e.kind == Kind::Variable && one_of(value, &["COLOR", "FLOAT", "STRING", "BOOLEAN"])
            }
            "value" => {
                e.kind == Kind::Variable
                    && match e.props.get("resolvedType").and_then(Value::as_str) {
                        Some("COLOR") => color(value),
                        Some("FLOAT") => number(value, -1e6, 1e6),
                        Some("STRING") => string(value, 65536),
                        Some("BOOLEAN") => value.is_boolean(),
                        _ => false,
                    }
            }
            "codeSyntax" => {
                e.kind == Kind::Variable
                    && value.as_object().is_some_and(|v| {
                        v.iter().all(|(k, v)| {
                            ["WEB", "ANDROID", "iOS"].contains(&k.as_str()) && string(v, 256)
                        })
                    })
            }
            "scopes" => {
                e.kind == Kind::Variable
                    && value.as_array().is_some_and(|v| {
                        !v.is_empty()
                            && v.len() <= 32
                            && v.iter().all(|s| {
                                one_of(
                                    s,
                                    &[
                                        "TEXT_CONTENT",
                                        "CORNER_RADIUS",
                                        "WIDTH_HEIGHT",
                                        "GAP",
                                        "ALL_FILLS",
                                        "FRAME_FILL",
                                        "SHAPE_FILL",
                                        "TEXT_FILL",
                                        "STROKE_COLOR",
                                        "STROKE_FLOAT",
                                        "OPACITY",
                                        "FONT_FAMILY",
                                        "FONT_STYLE",
                                        "FONT_WEIGHT",
                                        "FONT_SIZE",
                                        "LINE_HEIGHT",
                                        "LETTER_SPACING",
                                        "PARAGRAPH_SPACING",
                                        "PARAGRAPH_INDENT",
                                        "BOOLEAN",
                                    ],
                                )
                            })
                    })
            }
            "bindings" => value.as_object().is_some_and(|v| {
                v.len() <= 32
                    && v.iter().all(|(k, v)| {
                        [
                            "fills",
                            "cornerRadius",
                            "itemSpacing",
                            "paddingTop",
                            "paddingBottom",
                            "paddingLeft",
                            "paddingRight",
                            "width",
                            "height",
                            "visible",
                            "opacity",
                            "fontSize",
                        ]
                        .contains(&k.as_str())
                            && v.as_str().is_some_and(reference)
                    })
            }),
            "fillStyle" | "textStyle" => value.as_str().is_some_and(reference),
            // Asset IDs are local content hashes, never locators or arbitrary URLs.
            "image" => {
                e.kind == Kind::Rectangle
                    && value.as_str().is_some_and(|s| {
                        s.starts_with("sha256:")
                            && s.len() == 71
                            && s[7..].bytes().all(|b| b.is_ascii_hexdigit())
                    })
            }
            "imageScaleMode" => one_of(value, &["FILL", "FIT", "TILE"]),
            _ => false,
        };
        if !valid {
            return Err(Error::MalformedInput);
        }
    }
    if e.kind == Kind::Variable
        && !["collection", "resolvedType", "value", "scopes"]
            .iter()
            .all(|k| e.props.contains_key(*k))
    {
        return Err(Error::MalformedInput);
    }
    if e.kind == Kind::Instance && !e.props.contains_key("component")
        || e.kind == Kind::ComponentSet && !e.props.contains_key("variants")
    {
        return Err(Error::MalformedInput);
    }
    Ok(())
}
fn validate(entities: &[Entity], complete: bool) -> Result<BTreeMap<&str, &Entity>> {
    if entities.len() > MAX_NODES {
        return Err(Error::ResourceLimit);
    }
    let mut found = BTreeMap::new();
    for e in entities {
        if e.key.starts_with('@') || !reference(&e.key) || found.insert(e.key.as_str(), e).is_some()
        {
            return Err(Error::InvalidTarget);
        }
        validate_props(e)?;
        for r in e
            .parent
            .iter()
            .chain(e.page.iter())
            .chain(e.children.iter())
        {
            if !reference(r) {
                return Err(Error::InvalidTarget);
            }
        }
        if e.children.iter().collect::<BTreeSet<_>>().len() != e.children.len()
            || !e.children.is_empty() && !e.kind.container()
        {
            return Err(Error::InvalidTarget);
        }
    }
    for e in entities {
        let mut cursor = e;
        let mut visited = BTreeSet::new();
        while let Some(parent) = &cursor.parent {
            if parent.starts_with('@') {
                break;
            }
            if !visited.insert(parent) {
                return Err(Error::InvalidTarget);
            }
            if visited.len() > MAX_DEPTH {
                return Err(Error::ResourceLimit);
            }
            let Some(next) = found.get(parent.as_str()) else {
                if complete {
                    return Err(Error::InvalidTarget);
                } else {
                    break;
                }
            };
            cursor = next;
            if !cursor.kind.container() {
                return Err(Error::InvalidTarget);
            }
        }
        for child in &e.children {
            if child.starts_with('@') {
                continue;
            }
            if !complete && !found.contains_key(child.as_str()) {
                continue;
            }
            if found
                .get(child.as_str())
                .is_none_or(|c| c.parent.as_deref() != Some(&e.key))
            {
                return Err(Error::InvalidTarget);
            }
        }
    }
    Ok(found)
}

/// Topological ordering allows resource/component references before instances.
fn dependencies(e: &Entity) -> Vec<&str> {
    let mut result: Vec<_> = e
        .parent
        .iter()
        .chain(e.page.iter())
        .map(String::as_str)
        .collect();
    for key in ["component", "collection", "fillStyle", "textStyle"] {
        if let Some(v) = e.props.get(key).and_then(Value::as_str) {
            result.push(v);
        }
    }
    if let Some(v) = e.props.get("variants").and_then(Value::as_array) {
        result.extend(v.iter().filter_map(Value::as_str));
    }
    if let Some(v) = e.props.get("bindings").and_then(Value::as_object) {
        result.extend(v.values().filter_map(Value::as_str));
    }
    result
}

pub fn plan(input: Input) -> Result<Plan> {
    if !(512..=MAX_CODE_UNITS).contains(&input.payload_limit) {
        return Err(Error::ResourceLimit);
    }
    let desired = validate(&input.desired, true)?;
    let previous = validate(&input.previous, false)?;
    if input
        .bindings
        .iter()
        .any(|(key, id)| key.starts_with('@') || !reference(key) || !remote_id(id))
    {
        return Err(Error::InvalidTarget);
    }
    let mut pending = BTreeMap::new();
    for e in &input.desired {
        let old = previous.get(e.key.as_str()).copied();
        if let Some(old) = old {
            if old.kind != e.kind || old.parent != e.parent {
                return Err(Error::UnsupportedEdit);
            }
        }
        if old == Some(e) {
            continue;
        }
        pending.insert(
            e.key.as_str(),
            Operation {
                action: if input.bindings.contains_key(&e.key) {
                    Action::Update
                } else {
                    Action::Create
                },
                entity: e.clone(),
                previous: old.cloned(),
            },
        );
    }
    let mut operations = Vec::new();
    let mut available: BTreeSet<&str> = input.bindings.keys().map(String::as_str).collect();
    while !pending.is_empty() {
        let ready: Vec<_> = input
            .desired
            .iter()
            .filter(|e| {
                pending.contains_key(e.key.as_str())
                    && dependencies(e)
                        .iter()
                        .all(|d| d.starts_with('@') || available.contains(d))
            })
            .map(|e| e.key.as_str())
            .collect();
        if ready.is_empty() {
            return Err(Error::InvalidTarget);
        }
        for key in ready {
            operations.push(pending.remove(key).ok_or(Error::InvalidTarget)?);
            available.insert(key);
        }
    }
    // Deleting a parent is permitted only if every remote child is managed;
    // the executor checks that condition again immediately before removal.
    for old in input.previous.iter().rev() {
        if !desired.contains_key(old.key.as_str()) && input.bindings.contains_key(&old.key) {
            if old.kind.resource() || old.kind == Kind::Page {
                return Err(Error::UnsupportedEdit);
            }
            operations.push(Operation {
                action: Action::Delete,
                entity: old.clone(),
                previous: Some(old.clone()),
            });
        }
    }
    // A parent's first operation can precede new children. Reorder after all
    // creations, once those references exist, without moving foreign siblings.
    for entity in &input.desired {
        if !entity.children.is_empty()
            && previous
                .get(entity.key.as_str())
                .is_none_or(|old| old.children != entity.children)
        {
            operations.push(Operation {
                action: Action::Update,
                entity: entity.clone(),
                previous: Some(entity.clone()),
            });
        }
    }
    let operation_count = operations.len();
    let mut batches: Vec<Batch> = Vec::new();
    for op in operations {
        let page = op.entity.page.clone();
        let units = serde_json::to_string(&op)
            .map_err(|_| Error::MalformedInput)?
            .encode_utf16()
            .count();
        if units + 2 > input.payload_limit {
            return Err(Error::ResourceLimit);
        }
        let append = batches.last().is_some_and(|b| {
            b.page == page
                && b.operations.len() < 24
                && serde_json::to_string(&b.operations)
                    .map(|s| s.encode_utf16().count() + units + 1 <= input.payload_limit)
                    .unwrap_or(false)
        });
        if append {
            batches
                .last_mut()
                .ok_or(Error::MalformedInput)?
                .operations
                .push(op);
        } else {
            batches.push(Batch {
                page,
                operations: vec![op],
            });
        }
    }
    Ok(Plan {
        batches,
        operation_count,
    })
}

pub fn plan_json(json: &str) -> Result<String> {
    if json.len() > MAX_MODEL_BYTES {
        return Err(Error::ResourceLimit);
    }
    let input = serde_json::from_str(json).map_err(|_| Error::MalformedInput)?;
    serde_json::to_string(&plan(input)?).map_err(|_| Error::MalformedInput)
}
