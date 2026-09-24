use std::collections::BTreeMap;

use forge_figma::{Action, Entity, Error, Input, Kind, plan, plan_json};
use serde_json::json;
use uuid::Uuid;
fn node(kind: Kind, parent: Option<String>, page: Option<String>) -> Entity {
    Entity {
        key: Uuid::now_v7().to_string(),
        kind,
        parent,
        page,
        props: BTreeMap::from([("name".into(), json!("Example"))]),
        children: vec![],
    }
}
fn input(desired: Vec<Entity>) -> Input {
    Input {
        desired,
        previous: vec![],
        bindings: BTreeMap::new(),
        payload_limit: 4000,
    }
}
#[test]
fn orders_pages_resources_components_and_instances() {
    let page = node(Kind::Page, None, None);
    let component = node(
        Kind::Component,
        Some(page.key.clone()),
        Some(page.key.clone()),
    );
    let mut instance = node(
        Kind::Instance,
        Some(page.key.clone()),
        Some(page.key.clone()),
    );
    instance
        .props
        .insert("component".into(), json!(component.key));
    let p = plan(input(vec![instance, component, page])).unwrap();
    assert_eq!(p.operation_count, 3);
    let kinds: Vec<_> = p
        .batches
        .iter()
        .flat_map(|b| b.operations.iter().map(|o| o.entity.kind))
        .collect();
    assert_eq!(kinds, vec![Kind::Page, Kind::Component, Kind::Instance]);
}
#[test]
fn no_op_has_no_remote_operations() {
    let e = node(Kind::Text, Some("@1:2".into()), Some("@0:1".into()));
    let mut i = input(vec![e.clone()]);
    i.previous = vec![e.clone()];
    i.bindings.insert(e.key, "1:3".into());
    assert_eq!(plan(i).unwrap().operation_count, 0);
}
#[test]
fn explicit_external_target_updates_in_place() {
    let mut e = node(Kind::Text, Some("@1:2".into()), Some("@0:1".into()));
    e.props.insert("characters".into(), json!("Updated"));
    let mut i = input(vec![e.clone()]);
    i.bindings.insert(e.key, "1:3".into());
    assert_eq!(
        plan(i).unwrap().batches[0].operations[0].action,
        Action::Update
    );
}
#[test]
fn rejects_kind_replacement_and_reparenting() {
    let e = node(Kind::Frame, None, None);
    let mut changed = e.clone();
    changed.kind = Kind::Rectangle;
    let mut i = input(vec![changed]);
    i.previous = vec![e];
    assert_eq!(plan(i).unwrap_err(), Error::UnsupportedEdit);
}
#[test]
fn validates_paints_fonts_and_unknown_properties_before_writes() {
    for (key, value) in [
        (
            "fills",
            json!([{"type":"SOLID","color":{"r":255,"g":0,"b":0}}]),
        ),
        ("fontSize", json!(-2)),
        ("setPluginData", json!("no")),
    ] {
        let mut e = node(Kind::Text, None, None);
        e.props.insert(key.into(), value);
        assert_eq!(plan(input(vec![e])).unwrap_err(), Error::MalformedInput);
    }
}
#[test]
fn rejects_cyclic_or_missing_dependencies() {
    let mut e = node(Kind::Frame, None, None);
    e.parent = Some(e.key.clone());
    assert_eq!(plan(input(vec![e])).unwrap_err(), Error::InvalidTarget);
    let mut i = node(Kind::Instance, None, None);
    i.props
        .insert("component".into(), json!(Uuid::now_v7().to_string()));
    assert_eq!(plan(input(vec![i])).unwrap_err(), Error::InvalidTarget);
}
#[test]
fn batches_on_actual_utf16_units_and_pages() {
    let mut nodes = vec![];
    for _ in 0..10 {
        let mut n = node(Kind::Text, Some("@1:2".into()), Some("@0:1".into()));
        n.props.insert("characters".into(), json!("😀".repeat(200)));
        nodes.push(n);
    }
    let mut i = input(nodes);
    i.payload_limit = 1500;
    let plan = plan(i).unwrap();
    assert!(plan.batches.len() > 1);
    for batch in plan.batches {
        assert!(
            serde_json::to_string(&batch.operations)
                .unwrap()
                .encode_utf16()
                .count()
                <= 1500
        );
    }
}
#[test]
fn rejects_an_indivisible_oversize_operation() {
    let mut e = node(Kind::Text, None, None);
    e.props
        .insert("characters".into(), json!("x".repeat(10000)));
    assert_eq!(plan(input(vec![e])).unwrap_err(), Error::ResourceLimit);
}
#[test]
fn deletion_is_limited_to_bound_previous_entities() {
    let e = node(Kind::Rectangle, Some("@1:2".into()), Some("@0:1".into()));
    let mut i = input(vec![]);
    i.previous = vec![e.clone()];
    assert_eq!(plan(i).unwrap().operation_count, 0);
    let mut i = input(vec![]);
    i.previous = vec![e.clone()];
    i.bindings.insert(e.key, "1:3".into());
    assert_eq!(
        plan(i).unwrap().batches[0].operations[0].action,
        Action::Delete
    );
}
#[test]
fn refuses_resource_and_page_deletion() {
    for kind in [Kind::Page, Kind::Collection, Kind::PaintStyle] {
        let e = node(kind, None, None);
        let mut i = input(vec![]);
        i.previous = vec![e.clone()];
        i.bindings.insert(e.key, "1:2".into());
        assert_eq!(plan(i).unwrap_err(), Error::UnsupportedEdit);
    }
}
#[test]
fn json_boundary_is_bounded_and_strict() {
    assert_eq!(plan_json("{}"), Err(Error::MalformedInput));
    assert_eq!(
        plan_json(&" ".repeat(16 * 1024 * 1024 + 1)),
        Err(Error::ResourceLimit)
    );
}
