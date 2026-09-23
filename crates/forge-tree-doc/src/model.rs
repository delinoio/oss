use std::collections::BTreeMap;

use schemars::JsonSchema;
use serde::{Deserialize, Serialize};
use uuid::Uuid;

macro_rules! enums {
    ($name:ident { $first:ident $(, $rest:ident)* $(,)? }) => {
        #[derive(Debug, Clone, Copy, PartialEq, Eq, Default, Serialize, Deserialize, JsonSchema)]
        #[serde(rename_all = "snake_case")]
        pub enum $name { #[default] $first, $($rest),* }
    };
}
enums!(DocumentKind { Presentation });
enums!(PatchKind { Patch });
enums!(Unit { Pt });
enums!(NodeKind {
    Text,
    List,
    Image,
    Shape,
    Table,
    Chart,
    Connector,
    Row,
    Column,
    Canvas,
    Opaque
});
enums!(SizeMode { Hug, Fill });
enums!(Align {
    Left,
    Center,
    Right,
    Justify
});
enums!(Overflow { Error, Shrink });
enums!(ImageFit { Contain, Cover });
enums!(Shape {
    Rect,
    RoundedRect,
    Ellipse
});
enums!(Marker { Bullet, Number });
enums!(ChartType { Bar });
enums!(Orientation {
    Vertical,
    Horizontal
});
enums!(Legend {
    Hidden,
    Bottom,
    Right
});
enums!(DataLabels { Hidden, Value });
enums!(Anchor {
    Top,
    Right,
    Bottom,
    Left
});
enums!(ConnectorType { Straight, Elbow });
enums!(StyleProperty {
    FontFamily,
    FontSize,
    FontWeight,
    Color,
    ColorRef,
    Italic,
    Underline
});

#[derive(Debug, Clone, Copy, PartialEq, Serialize, Deserialize, JsonSchema)]
#[serde(untagged)]
pub enum Size {
    Points(f64),
    Mode(SizeMode),
}
impl Size {
    pub fn fixed(self) -> Option<f64> {
        match self {
            Self::Points(v) => Some(v),
            _ => None,
        }
    }

    pub fn is_fill(self) -> bool {
        self == Self::Mode(SizeMode::Fill)
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Default, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Frame {
    pub x: f64,
    pub y: f64,
    pub width: f64,
    pub height: f64,
}
#[derive(Debug, Clone, Copy, PartialEq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Page {
    pub width: f64,
    pub height: f64,
}
impl Default for Page {
    fn default() -> Self {
        Self {
            width: 960.0,
            height: 540.0,
        }
    }
}

#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Color {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub color: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub color_ref: Option<String>,
}
#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct TextStyle {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub font_family: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub font_size: Option<f64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub font_weight: Option<u16>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub color: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub color_ref: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub italic: Option<bool>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub underline: Option<bool>,
}
impl TextStyle {
    pub fn overlay(&mut self, other: &Self) {
        macro_rules! merge { ($($f:ident),*) => { $(if other.$f.is_some() { self.$f = other.$f.clone(); })* }; }
        merge!(font_family, font_size, font_weight, italic, underline);
        if other.color.is_some() || other.color_ref.is_some() {
            self.color = other.color.clone();
            self.color_ref = other.color_ref.clone();
        }
    }

    pub fn unset(&mut self, props: &[StyleProperty]) {
        for p in props {
            match p {
                StyleProperty::FontFamily => self.font_family = None,
                StyleProperty::FontSize => self.font_size = None,
                StyleProperty::FontWeight => self.font_weight = None,
                StyleProperty::Color => self.color = None,
                StyleProperty::ColorRef => self.color_ref = None,
                StyleProperty::Italic => self.italic = None,
                StyleProperty::Underline => self.underline = None,
            }
        }
    }
}
fn default_font() -> String {
    "Noto Sans KR".into()
}
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Theme {
    #[serde(default = "default_font")]
    pub font_family: String,
    #[serde(default)]
    pub colors: BTreeMap<String, String>,
    #[serde(default)]
    pub text_styles: BTreeMap<String, TextStyle>,
}
impl Default for Theme {
    fn default() -> Self {
        Self {
            font_family: default_font(),
            colors: BTreeMap::new(),
            text_styles: BTreeMap::new(),
        }
    }
}
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Run {
    pub text: String,
    #[serde(default)]
    pub style: TextStyle,
}
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Paragraph {
    #[serde(default)]
    pub align: Align,
    pub runs: Vec<Run>,
}
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct AssetRef {
    pub handle: String,
}
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Column {
    pub width: Size,
}
fn one() -> usize {
    1
}
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Cell {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub text: Option<String>,
    #[serde(default)]
    pub paragraphs: Vec<Paragraph>,
    #[serde(default)]
    pub style: TextStyle,
    #[serde(default)]
    pub fill: Color,
    #[serde(default = "one")]
    pub row_span: usize,
    #[serde(default = "one")]
    pub col_span: usize,
}
impl Default for Cell {
    fn default() -> Self {
        Self {
            text: None,
            paragraphs: Vec::new(),
            style: TextStyle::default(),
            fill: Color::default(),
            row_span: 1,
            col_span: 1,
        }
    }
}
#[derive(Debug, Clone, Copy, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct CellAddress {
    pub row: usize,
    pub column: usize,
}
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct TableRow {
    pub cells: Vec<Cell>,
}
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Series {
    pub key: String,
    pub name: String,
    pub values: Vec<f64>,
}
#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct ChartData {
    pub categories: Vec<String>,
    pub series: Vec<Series>,
}
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Target {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub key: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub node_id: Option<Uuid>,
}
impl Target {
    pub fn matches(&self, n: &Node) -> bool {
        self.key.as_ref().is_some_and(|k| n.key.as_ref() == Some(k))
            || self.node_id.is_some_and(|i| n.id == Some(i))
    }
}
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Endpoint {
    pub target: Target,
    pub anchor: Anchor,
}

/// Closed field vocabulary shared by node variants. Variant-specific field
/// legality is checked by validation in addition to serde's unknown-field
/// rejection.
#[derive(Debug, Clone, PartialEq, Default, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Node {
    #[serde(rename = "type")]
    pub kind: NodeKind,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub id: Option<Uuid>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub key: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub width: Option<Size>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub height: Option<Size>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub frame: Option<Frame>,
    #[serde(default)]
    pub padding: f64,
    #[serde(default)]
    pub gap: f64,
    #[serde(default)]
    pub children: Vec<Node>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub style_ref: Option<String>,
    #[serde(default)]
    pub style: TextStyle,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub text: Option<String>,
    #[serde(default)]
    pub paragraphs: Vec<Paragraph>,
    #[serde(default)]
    pub items: Vec<String>,
    #[serde(default)]
    pub marker: Marker,
    #[serde(default)]
    pub overflow: Overflow,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub min_font_size: Option<f64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub asset_ref: Option<String>,
    #[serde(default)]
    pub fit: ImageFit,
    #[serde(default)]
    pub alt: String,
    #[serde(default)]
    pub shape: Shape,
    #[serde(default)]
    pub fill: Color,
    #[serde(default)]
    pub columns: Vec<Column>,
    #[serde(default)]
    pub rows: Vec<TableRow>,
    #[serde(default)]
    pub chart_type: ChartType,
    #[serde(default)]
    pub orientation: Orientation,
    #[serde(default)]
    pub legend: Legend,
    #[serde(default)]
    pub data_labels: DataLabels,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub data: Option<ChartData>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub from: Option<Endpoint>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub to: Option<Endpoint>,
    #[serde(default)]
    pub connector_type: ConnectorType,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub placeholder_ref: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub opaque_ref: Option<String>,
}
impl Node {
    pub fn is_container(&self) -> bool {
        matches!(
            self.kind,
            NodeKind::Row | NodeKind::Column | NodeKind::Canvas
        )
    }

    pub fn paragraphs(&self) -> Vec<Paragraph> {
        if self.kind == NodeKind::List {
            return self
                .items
                .iter()
                .map(|text| Paragraph {
                    align: Align::Left,
                    runs: vec![Run {
                        text: text.clone(),
                        style: TextStyle::default(),
                    }],
                })
                .collect();
        }
        if let Some(text) = &self.text {
            return text
                .split('\n')
                .map(|text| Paragraph {
                    align: Align::Left,
                    runs: vec![Run {
                        text: text.into(),
                        style: TextStyle::default(),
                    }],
                })
                .collect();
        }
        self.paragraphs.clone()
    }

    pub fn visit<'a>(&'a self, f: &mut impl FnMut(&'a Node)) {
        f(self);
        for child in &self.children {
            child.visit(f);
        }
    }

    pub fn visit_mut(&mut self, f: &mut impl FnMut(&mut Node)) {
        f(self);
        for child in &mut self.children {
            child.visit_mut(f);
        }
    }

    pub fn find(&self, target: &Target) -> Option<&Node> {
        if target.matches(self) {
            Some(self)
        } else {
            self.children.iter().find_map(|c| c.find(target))
        }
    }

    pub fn find_mut(&mut self, target: &Target) -> Option<&mut Node> {
        if target.matches(self) {
            Some(self)
        } else {
            self.children.iter_mut().find_map(|c| c.find_mut(target))
        }
    }
}
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Slide {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub id: Option<Uuid>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub key: Option<String>,
    #[serde(default)]
    pub background: Color,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub slide_layout_ref: Option<String>,
    pub content: Node,
}
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Presentation {
    pub dsl_version: u32,
    pub kind: DocumentKind,
    #[serde(default)]
    pub unit: Unit,
    #[serde(default)]
    pub page: Page,
    #[serde(default)]
    pub theme: Theme,
    #[serde(default)]
    pub assets: BTreeMap<String, AssetRef>,
    pub slides: Vec<Slide>,
}
impl Presentation {
    pub fn assign_ids(&mut self) {
        for slide in &mut self.slides {
            slide.id.get_or_insert_with(Uuid::now_v7);
            slide.content.visit_mut(&mut |n| {
                n.id.get_or_insert_with(Uuid::now_v7);
            });
        }
    }

    pub fn find(&self, target: &Target) -> Option<&Node> {
        self.slides.iter().find_map(|s| s.content.find(target))
    }

    pub fn find_mut(&mut self, target: &Target) -> Option<&mut Node> {
        self.slides
            .iter_mut()
            .find_map(|s| s.content.find_mut(target))
    }

    pub fn style(&self, node: &Node) -> TextStyle {
        let mut style = TextStyle {
            font_family: Some(self.theme.font_family.clone()),
            font_size: Some(20.0),
            font_weight: Some(400),
            color: Some("#172033".into()),
            ..Default::default()
        };
        if let Some(reference) = &node.style_ref
            && let Some(named) = self.theme.text_styles.get(reference)
        {
            style.overlay(named);
        }
        style.overlay(&node.style);
        style
    }

    pub fn resolve_color(&self, color: &Color, default: &str) -> String {
        color
            .color
            .clone()
            .or_else(|| {
                color
                    .color_ref
                    .as_ref()
                    .and_then(|key| self.theme.colors.get(key).cloned())
            })
            .unwrap_or_else(|| default.into())
    }
}
