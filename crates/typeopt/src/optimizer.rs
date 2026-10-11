//! Deterministic allocation for object regions proven closed by the compiler
//! adapter.
use std::collections::BTreeSet;

use serde::Serialize;

#[derive(Clone, Debug, Serialize)]
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
}

#[derive(Clone, Debug)]
pub struct Occurrence {
    pub start: usize,
    pub end: usize,
    pub shorthand: bool,
    pub quoted: bool,
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

#[derive(Clone, Debug)]
pub struct Edit {
    pub start: usize,
    pub end: usize,
    pub replacement: String,
}

/// Each closed object is a clique: its own properties must have distinct names.
/// Different closed objects have no shared references or observable shape and
/// therefore have no interference edges. Unsafe objects are never allocated.
pub fn allocate(
    objects: &mut [Object],
) -> (Vec<Mapping>, std::collections::BTreeMap<String, Vec<Edit>>) {
    objects.sort_by(|a, b| (&a.file, a.start).cmp(&(&b.file, b.start)));
    let mut mappings = Vec::new();
    let mut edits = std::collections::BTreeMap::<String, Vec<Edit>>::new();
    for object in objects {
        // Frequency first, with a stable name tie-breaker. Reserve source names
        // too so unchanged short keys can never collide with an allocated key.
        let mut reserved: BTreeSet<String> =
            object.properties.iter().map(|p| p.name.clone()).collect();
        object.properties.sort_by(|a, b| {
            b.occurrences
                .len()
                .cmp(&a.occurrences.len())
                .then(a.name.cmp(&b.name))
        });
        let mut next = 0;
        for property in &object.properties {
            let mut reason = object.preservation.clone();
            let mut renamed = property.name.clone();
            if reason.is_none() {
                let candidate = loop {
                    let name = short_name(next);
                    next += 1;
                    if !reserved.contains(&name) {
                        break name;
                    }
                };
                let old_size: usize = property.occurrences.iter().map(|o| o.end - o.start).sum();
                let new_size: usize = property
                    .occurrences
                    .iter()
                    .map(|o| {
                        candidate.len()
                            + if o.shorthand {
                                2 + property.name.len()
                            } else if o.quoted {
                                2
                            } else {
                                0
                            }
                    })
                    .sum();
                if new_size < old_size {
                    renamed = candidate;
                    reserved.insert(renamed.clone());
                    for occurrence in &property.occurrences {
                        edits.entry(object.file.clone()).or_default().push(Edit {
                            start: occurrence.start,
                            end: occurrence.end,
                            replacement: if occurrence.shorthand {
                                format!("{renamed}: {}", property.name)
                            } else if occurrence.quoted {
                                format!("\"{renamed}\"")
                            } else {
                                renamed.clone()
                            },
                        });
                    }
                } else {
                    reason = Some(Preservation::NoSizeBenefit);
                }
            }
            mappings.push(Mapping {
                group: format!("{}:{}:{}", object.file, object.start, property.name),
                source: object.file.clone(),
                original: property.name.clone(),
                renamed,
                occurrences: property.occurrences.len(),
                preservation: reason,
            });
        }
    }
    for file_edits in edits.values_mut() {
        file_edits.sort_by_key(|e| e.start);
    }
    (mappings, edits)
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
    fn property(name: &str, count: usize) -> Property {
        Property {
            name: name.into(),
            occurrences: (0..count)
                .map(|i| Occurrence {
                    start: i * 30,
                    end: i * 30 + name.len(),
                    shorthand: false,
                    quoted: false,
                })
                .collect(),
        }
    }
    #[test]
    fn allocation_disambiguates_and_reuses_without_collisions() {
        let mut objects = vec![
            Object {
                file: "input.ts".into(),
                start: 0,
                properties: vec![property("sharedLong", 2), property("otherLong", 1)],
                preservation: None,
            },
            Object {
                file: "input.ts".into(),
                start: 100,
                properties: vec![property("sharedLong", 1), property("otherLong", 2)],
                preservation: None,
            },
        ];
        let (m, _) = allocate(&mut objects);
        assert_eq!((&m[0].renamed, &m[1].renamed), (&"a".into(), &"b".into()));
        assert_eq!(m[0].original, m[3].original);
        assert_ne!(m[0].renamed, m[3].renamed);
        assert_eq!(m[0].renamed, m[2].renamed);
    }
}
