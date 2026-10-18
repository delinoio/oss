//! Deterministic property-name allocation for object regions proven closed by
//! the compiler adapter.
use std::collections::{BTreeMap, BTreeSet};

use serde::Serialize;

use crate::plan::Edit;

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum Preservation {
    ExternalOrUnsupportedShape,
    Exported,
    Escapes,
    DynamicAccess,
    ObservableOperation,
    TypeConstraint,
    SpecialProperty,
    NoSizeBenefit,
    /// The rename needs a disabled property pass.
    RequiresDisabledPass,
    /// The renamed program failed type checking and was rolled back.
    RecheckFailed,
}

#[derive(Clone, Debug)]
pub struct Occurrence {
    /// Module containing the occurrence. Object regions can span modules.
    pub file: String,
    pub start: usize,
    pub end: usize,
    pub shorthand: bool,
    pub quoted: bool,
    /// Type-only occurrence (for example an inline parameter type) that is
    /// renamed for consistency but removed from emitted JavaScript.
    pub erased: bool,
    /// String literal whose runtime value reaches computed property keys
    /// only (finite-key coordination).
    pub key: bool,
}

#[derive(Clone, Debug)]
pub struct Property {
    pub name: String,
    pub occurrences: Vec<Occurrence>,
}

#[derive(Clone, Debug)]
pub struct Object {
    pub file: String,
    pub start: usize,
    pub properties: Vec<Property>,
    pub preservation: Option<Preservation>,
}

#[derive(Clone, Debug, Serialize)]
pub struct Mapping {
    pub group: String,
    pub source: String,
    pub original: String,
    pub renamed: String,
    pub occurrences: usize,
    pub preservation: Option<Preservation>,
}

/// How groups relate when allocating names.
#[derive(Clone, Copy, Debug)]
pub struct Strategy {
    /// Give unrelated same-named properties independent names.
    pub disambiguate: bool,
    /// Reuse short names across noninterfering groups.
    pub ambiguate: bool,
}

/// One rename unit: every edit of one or more property groups plus their
/// mappings. Units never interfere with each other, so the pipeline may
/// accept or reject each one independently.
#[derive(Clone, Debug)]
pub struct Allocation {
    pub key: String,
    pub file: String,
    pub start: usize,
    pub edits: Vec<(String, Edit)>,
    pub mappings: Vec<Mapping>,
    /// The unit renames a property whose original name also belongs to
    /// another object region; without disambiguation both would form one
    /// name-based identity.
    pub separated: bool,
    /// The unit receives a name that another region also uses; without
    /// ambiguation every new name is globally unique.
    pub reused: bool,
    /// The unit renames string-literal keys of finite computed accesses.
    pub finite_keys: bool,
}

struct Group {
    /// Indices of (object, property) pairs that form this identity.
    members: Vec<(usize, usize)>,
    preservation: Option<Preservation>,
    unit: usize,
}

/// Allocates short names. Each closed object is a clique: its own properties
/// must have distinct names. Different closed objects have no shared
/// references or observable shape and therefore have no interference edges.
/// Unsafe objects are never renamed; they only report preserved mappings.
///
/// Without disambiguation, every object that shares an original name forms
/// one name-based identity: a preservation anywhere preserves that name
/// everywhere, and all its occurrences receive one new name. Without
/// ambiguation, every renamed identity receives a globally unique name.
pub fn allocate(objects: &mut [Object], strategy: Strategy) -> (Vec<Allocation>, Vec<Mapping>) {
    objects.sort_by(|a, b| (&a.file, a.start).cmp(&(&b.file, b.start)));
    for object in objects.iter_mut() {
        // Frequency first, with a stable name tie-breaker.
        object.properties.sort_by(|a, b| {
            b.occurrences
                .len()
                .cmp(&a.occurrences.len())
                .then(a.name.cmp(&b.name))
        });
    }
    // Build identities.
    let mut groups: Vec<Group> = Vec::new();
    let mut by_name = BTreeMap::<&str, usize>::new();
    for (o, object) in objects.iter().enumerate() {
        for (p, property) in object.properties.iter().enumerate() {
            let index = if strategy.disambiguate {
                None
            } else {
                by_name.get(property.name.as_str()).copied()
            };
            let index = index.unwrap_or_else(|| {
                groups.push(Group {
                    members: Vec::new(),
                    preservation: None,
                    unit: if strategy.disambiguate {
                        o
                    } else {
                        groups.len()
                    },
                });
                if !strategy.disambiguate {
                    by_name.insert(&property.name, groups.len() - 1);
                }
                groups.len() - 1
            });
            groups[index].members.push((o, p));
            if let Some(reason) = &object.preservation {
                groups[index].preservation = Some(reason.clone());
            }
        }
    }
    // Every original name is reserved within its objects (or globally
    // without ambiguation), so an unchanged key never collides with an
    // allocated one.
    let all_names: BTreeSet<&str> = objects
        .iter()
        .flat_map(|o| o.properties.iter().map(|p| p.name.as_str()))
        .collect();
    let mut taken: Vec<BTreeSet<String>> = vec![BTreeSet::new(); objects.len()];
    let mut used_globally = BTreeSet::<String>::new();
    // Deterministic priority: most occurrences first, then first member.
    let mut order: Vec<usize> = (0..groups.len()).collect();
    let count = |g: &Group| -> usize {
        g.members
            .iter()
            .map(|&(o, p)| objects[o].properties[p].occurrences.len())
            .sum()
    };
    order.sort_by(|&a, &b| {
        count(&groups[b])
            .cmp(&count(&groups[a]))
            .then(groups[a].members[0].cmp(&groups[b].members[0]))
    });
    let mut renamed = vec![None::<String>; groups.len()];
    let mut reasons: Vec<Option<Preservation>> =
        groups.iter().map(|g| g.preservation.clone()).collect();
    for &g in &order {
        if reasons[g].is_some() {
            continue;
        }
        let group = &groups[g];
        let original = &objects[group.members[0].0].properties[group.members[0].1].name;
        let mut n = 0;
        let candidate = loop {
            let name = short_name(n);
            n += 1;
            let free = if strategy.ambiguate {
                group.members.iter().all(|&(o, _)| {
                    !taken[o].contains(&name)
                        && !objects[o].properties.iter().any(|p| p.name == name)
                })
            } else {
                !used_globally.contains(&name) && !all_names.contains(name.as_str())
            };
            if free {
                break name;
            }
        };
        // Only emitted occurrences affect output size; erased type spans
        // must not make a rename look profitable.
        let mut old_size = 0;
        let mut new_size = 0;
        for &(o, p) in &group.members {
            for occurrence in objects[o].properties[p]
                .occurrences
                .iter()
                .filter(|o| !o.erased)
            {
                old_size += occurrence.end - occurrence.start;
                new_size += candidate.len()
                    + if occurrence.shorthand {
                        2 + original.len()
                    } else if occurrence.quoted {
                        2
                    } else {
                        0
                    };
            }
        }
        if new_size >= old_size {
            reasons[g] = Some(Preservation::NoSizeBenefit);
            continue;
        }
        for &(o, _) in &group.members {
            taken[o].insert(candidate.clone());
        }
        used_globally.insert(candidate.clone());
        renamed[g] = Some(candidate);
    }
    // Attribution facts. An original name shared by several objects is only
    // independent with disambiguation; a final name used by several groups
    // (or equal to another object's retained name) is only available with
    // ambiguation.
    let mut name_objects = BTreeMap::<&str, BTreeSet<usize>>::new();
    for (o, object) in objects.iter().enumerate() {
        for property in &object.properties {
            name_objects.entry(&property.name).or_default().insert(o);
        }
    }
    let mut final_names = BTreeMap::<String, BTreeSet<usize>>::new();
    for (g, group) in groups.iter().enumerate() {
        let (o, p) = group.members[0];
        let name = renamed[g]
            .clone()
            .unwrap_or_else(|| objects[o].properties[p].name.clone());
        final_names.entry(name).or_default().insert(g);
    }
    let mut units = BTreeMap::<usize, Allocation>::new();
    let mut preserved = Vec::new();
    for (g, group) in groups.iter().enumerate() {
        for &(o, p) in &group.members {
            let object = &objects[o];
            let property = &object.properties[p];
            let mapping = Mapping {
                group: format!("{}:{}:{}", object.file, object.start, property.name),
                source: object.file.clone(),
                original: property.name.clone(),
                renamed: renamed[g].clone().unwrap_or_else(|| property.name.clone()),
                occurrences: property.occurrences.len(),
                preservation: reasons[g].clone(),
            };
            let Some(new_name) = &renamed[g] else {
                preserved.push(mapping);
                continue;
            };
            let unit = units.entry(group.unit).or_insert_with(|| Allocation {
                key: if strategy.disambiguate {
                    format!("property:{}:{}", object.file, object.start)
                } else {
                    format!("property-name:{}", property.name)
                },
                file: object.file.clone(),
                start: object.start,
                edits: Vec::new(),
                mappings: Vec::new(),
                separated: false,
                reused: false,
                finite_keys: false,
            });
            unit.separated |= strategy.disambiguate
                && name_objects
                    .get(property.name.as_str())
                    .is_some_and(|set| set.len() > 1);
            unit.reused |= strategy.ambiguate
                && (final_names.get(new_name).is_some_and(|set| set.len() > 1)
                    || name_objects
                        .get(new_name.as_str())
                        .is_some_and(|set| set.iter().any(|&other| other != o)));
            for occurrence in &property.occurrences {
                unit.finite_keys |= occurrence.key;
                unit.edits.push((
                    occurrence.file.clone(),
                    Edit {
                        start: occurrence.start,
                        end: occurrence.end,
                        replacement: if occurrence.shorthand {
                            format!("{new_name}: {}", property.name)
                        } else if occurrence.quoted {
                            format!("\"{new_name}\"")
                        } else {
                            new_name.clone()
                        },
                    },
                ));
            }
            unit.mappings.push(mapping);
        }
    }
    let mut allocations: Vec<_> = units.into_values().collect();
    for allocation in &mut allocations {
        allocation.edits.sort();
        allocation.edits.dedup();
    }
    (allocations, preserved)
}

fn short_name(mut n: usize) -> String {
    const LETTERS: &[u8] = b"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ$_";
    let mut result = String::new();
    loop {
        result.push(LETTERS[n % LETTERS.len()] as char);
        n /= LETTERS.len();
        if n == 0 {
            return result;
        }
        n -= 1;
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn property(name: &str, count: usize, base: usize) -> Property {
        Property {
            name: name.into(),
            occurrences: (0..count)
                .map(|i| Occurrence {
                    file: "input.ts".into(),
                    start: base + i * 30,
                    end: base + i * 30 + name.len(),
                    shorthand: false,
                    quoted: false,
                    erased: false,
                    key: false,
                })
                .collect(),
        }
    }
    fn objects() -> Vec<Object> {
        vec![
            Object {
                file: "input.ts".into(),
                start: 0,
                properties: vec![property("sharedLong", 2, 0), property("otherLong", 1, 60)],
                preservation: None,
            },
            Object {
                file: "input.ts".into(),
                start: 100,
                properties: vec![
                    property("sharedLong", 1, 100),
                    property("otherLong", 2, 130),
                ],
                preservation: None,
            },
        ]
    }
    fn renamed(allocations: &[Allocation]) -> Vec<(String, String)> {
        allocations
            .iter()
            .flat_map(|a| a.mappings.iter())
            .map(|m| (m.original.clone(), m.renamed.clone()))
            .collect()
    }

    #[test]
    fn allocation_disambiguates_and_reuses_without_collisions() {
        let (allocations, _) = allocate(
            &mut objects(),
            Strategy {
                disambiguate: true,
                ambiguate: true,
            },
        );
        let m = renamed(&allocations);
        assert_eq!(m[0], ("sharedLong".into(), "a".into()));
        assert_eq!(m[1], ("otherLong".into(), "b".into()));
        assert_eq!(m[2], ("otherLong".into(), "a".into()));
        assert_eq!(m[3], ("sharedLong".into(), "b".into()));
    }

    #[test]
    fn without_ambiguation_names_are_globally_unique() {
        let (allocations, _) = allocate(
            &mut objects(),
            Strategy {
                disambiguate: true,
                ambiguate: false,
            },
        );
        let names: BTreeSet<_> = renamed(&allocations).into_iter().map(|m| m.1).collect();
        assert_eq!(names.len(), 4);
    }

    #[test]
    fn without_disambiguation_names_are_shared_by_identity() {
        let (allocations, _) = allocate(
            &mut objects(),
            Strategy {
                disambiguate: false,
                ambiguate: true,
            },
        );
        let m = renamed(&allocations);
        let shared: BTreeSet<_> = m
            .iter()
            .filter(|(o, _)| o == "sharedLong")
            .map(|(_, r)| r)
            .collect();
        assert_eq!(shared.len(), 1);
        assert_eq!(allocations.len(), 2);
        let mut preserved = objects();
        preserved[1].preservation = Some(Preservation::Escapes);
        let (allocations, kept) = allocate(
            &mut preserved,
            Strategy {
                disambiguate: false,
                ambiguate: true,
            },
        );
        assert!(allocations.is_empty());
        assert_eq!(kept.len(), 4);
    }

    #[test]
    fn attribution_reflects_the_shaping_passes() {
        let all = Strategy {
            disambiguate: true,
            ambiguate: true,
        };
        // Shared original names need disambiguation; reused names need
        // ambiguation.
        let (allocations, _) = allocate(&mut objects(), all);
        assert!(allocations.iter().all(|a| a.separated && a.reused));
        // Unique names in one object need neither.
        let mut single = vec![Object {
            file: "input.ts".into(),
            start: 0,
            properties: vec![property("onlyLongName", 2, 0)],
            preservation: None,
        }];
        let (allocations, _) = allocate(&mut single, all);
        assert_eq!(allocations.len(), 1);
        assert!(!allocations[0].separated && !allocations[0].reused);
        assert!(!allocations[0].finite_keys);
        // Disabled passes never contribute.
        let (allocations, _) = allocate(
            &mut objects(),
            Strategy {
                disambiguate: false,
                ambiguate: false,
            },
        );
        assert!(allocations.iter().all(|a| !a.separated && !a.reused));
        // Finite-key literal occurrences are recorded.
        let mut keyed = single.clone();
        keyed[0].properties[0].occurrences[1].key = true;
        let (allocations, _) = allocate(&mut keyed, all);
        assert!(allocations[0].finite_keys);
        assert_eq!(allocations[0].edits[1].1.replacement, "a");
    }
}
