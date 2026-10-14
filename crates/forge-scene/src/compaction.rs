//! Explicit, bit-exact remapping of indexed geometry. No face is discarded.
use std::{
    collections::{HashMap, hash_map::DefaultHasher},
    hash::{Hash, Hasher},
};

use crate::{Geometry, MAX_GEOMETRY_BYTES, Result, checkpoint, geometry, limited};

pub struct CompactedGeometry {
    pub bytes: Vec<u8>,
    pub input_vertices: u32,
    pub output_vertices: u32,
    pub triangles: u32,
}

/// A conservative bound for operation-owned native buffers and the tuple map.
/// The source copy, decoded input and output, serialized buffers, result
/// validation, and hash buckets fit within this budget. Callers reserve this
/// amount before starting the worker.
pub fn compaction_scratch_bytes(bytes: &[u8]) -> Result<usize> {
    if bytes.len() > MAX_GEOMETRY_BYTES {
        return Err(limited());
    }
    let base = if bytes.starts_with(b"FSG2") {
        bytes.get(16..).ok_or_else(limited)?
    } else {
        bytes
    };
    if !base.starts_with(b"FSG1") || base.len() < 16 {
        return Err(crate::invalid("geometry"));
    }
    let count = |start: usize| -> Result<usize> {
        Ok(u32::from_le_bytes(
            base.get(start..start + 4)
                .ok_or_else(limited)?
                .try_into()
                .map_err(|_| limited())?,
        ) as usize)
    };
    let vertices = count(8)?;
    let indices = count(12)?;
    let scratch = bytes
        .len()
        .checked_mul(5)
        .and_then(|n| vertices.checked_mul(80).and_then(|v| n.checked_add(v)))
        .and_then(|n| indices.checked_mul(8).and_then(|i| n.checked_add(i)))
        .ok_or_else(limited)?;
    if scratch > crate::MAX_BYTES {
        return Err(limited());
    }
    Ok(scratch)
}

fn float_bits_equal<const N: usize>(a: &[f32; N], b: &[f32; N]) -> bool {
    a.iter().zip(b).all(|(a, b)| a.to_bits() == b.to_bits())
}

fn same_tuple(g: &Geometry, a: usize, b: usize) -> bool {
    float_bits_equal(&g.positions[a], &g.positions[b])
        && float_bits_equal(&g.normals[a], &g.normals[b])
        && g.tangents
            .as_ref()
            .is_none_or(|v| float_bits_equal(&v[a], &v[b]))
        && g.uv.as_ref().is_none_or(|v| float_bits_equal(&v[a], &v[b]))
        && g.skin.as_ref().is_none_or(|skin| {
            skin.joints[a] == skin.joints[b] && float_bits_equal(&skin.weights[a], &skin.weights[b])
        })
        && g.morph_targets.iter().all(|target| {
            float_bits_equal(&target.positions[a], &target.positions[b])
                && target
                    .normals
                    .as_ref()
                    .is_none_or(|v| float_bits_equal(&v[a], &v[b]))
        })
}

fn tuple_hash(g: &Geometry, index: usize) -> u64 {
    let mut hash = DefaultHasher::new();
    for value in g.positions[index].iter().chain(&g.normals[index]) {
        value.to_bits().hash(&mut hash);
    }
    if let Some(values) = &g.tangents {
        for value in values[index] {
            value.to_bits().hash(&mut hash);
        }
    }
    if let Some(values) = &g.uv {
        for value in values[index] {
            value.to_bits().hash(&mut hash);
        }
    }
    if let Some(skin) = &g.skin {
        skin.joints[index].hash(&mut hash);
        for value in skin.weights[index] {
            value.to_bits().hash(&mut hash);
        }
    }
    for target in &g.morph_targets {
        for value in target.positions[index] {
            value.to_bits().hash(&mut hash);
        }
        if let Some(normals) = &target.normals {
            for value in normals[index] {
                value.to_bits().hash(&mut hash);
            }
        }
    }
    hash.finish()
}

fn remap_with_hash(g: &Geometry, hash: impl Fn(&Geometry, usize) -> u64) -> Result<Geometry> {
    let mut buckets: HashMap<u64, Vec<(usize, u32)>> = HashMap::new();
    let mut first_references = Vec::new();
    let mut indices = Vec::with_capacity(g.indices.len());
    for (visited, &source) in g.indices.iter().enumerate() {
        if visited % 4096 == 0 {
            checkpoint()?;
        }
        let source = source as usize;
        let candidates = buckets.entry(hash(g, source)).or_default();
        let mapped = candidates
            .iter()
            .find(|&&(candidate, _)| same_tuple(g, candidate, source))
            .map(|&(_, mapped)| mapped);
        let mapped = if let Some(mapped) = mapped {
            mapped
        } else {
            let next = u32::try_from(first_references.len()).map_err(|_| limited())?;
            candidates.push((source, next));
            first_references.push(source);
            next
        };
        indices.push(mapped);
    }
    fn collect<T: Copy>(values: &[T], references: &[usize]) -> Result<Vec<T>> {
        let mut output = Vec::with_capacity(references.len());
        for (visited, &index) in references.iter().enumerate() {
            if visited % 4096 == 0 {
                checkpoint()?;
            }
            output.push(values[index]);
        }
        Ok(output)
    }
    let positions = collect(&g.positions, &first_references)?;
    let normals = collect(&g.normals, &first_references)?;
    let tangents = g
        .tangents
        .as_ref()
        .map(|v| collect(v, &first_references))
        .transpose()?;
    let uv =
        g.uv.as_ref()
            .map(|v| collect(v, &first_references))
            .transpose()?;
    let skin = g
        .skin
        .as_ref()
        .map(|skin| -> Result<_> {
            Ok(crate::VertexSkin {
                joints: collect(&skin.joints, &first_references)?,
                weights: collect(&skin.weights, &first_references)?,
            })
        })
        .transpose()?;
    let morph_targets = g
        .morph_targets
        .iter()
        .map(|target| -> Result<_> {
            Ok(crate::MorphTarget {
                name: target.name.clone(),
                positions: collect(&target.positions, &first_references)?,
                normals: target
                    .normals
                    .as_ref()
                    .map(|v| collect(v, &first_references))
                    .transpose()?,
            })
        })
        .collect::<Result<Vec<_>>>()?;
    Ok(Geometry {
        positions,
        normals,
        tangents,
        uv,
        indices,
        skin,
        morph_targets,
    })
}

fn encode(g: &Geometry) -> Result<Vec<u8>> {
    let mut base = Vec::new();
    base.extend_from_slice(b"FSG1");
    base.extend_from_slice(&u32::to_le_bytes(
        u32::from(g.uv.is_some()) | (u32::from(g.tangents.is_some()) << 1),
    ));
    base.extend_from_slice(
        &u32::try_from(g.positions.len())
            .map_err(|_| limited())?
            .to_le_bytes(),
    );
    base.extend_from_slice(
        &u32::try_from(g.indices.len())
            .map_err(|_| limited())?
            .to_le_bytes(),
    );
    let mut floats = |values: &[f32]| {
        for value in values {
            base.extend_from_slice(&value.to_le_bytes());
        }
    };
    for (i, row) in g.positions.iter().enumerate() {
        if i % 4096 == 0 {
            checkpoint()?;
        }
        floats(row);
    }
    for (i, row) in g.normals.iter().enumerate() {
        if i % 4096 == 0 {
            checkpoint()?;
        }
        floats(row);
    }
    if let Some(rows) = &g.tangents {
        for (i, row) in rows.iter().enumerate() {
            if i % 4096 == 0 {
                checkpoint()?;
            }
            floats(row);
        }
    }
    if let Some(rows) = &g.uv {
        for (i, row) in rows.iter().enumerate() {
            if i % 4096 == 0 {
                checkpoint()?;
            }
            floats(row);
        }
    }
    for (i, index) in g.indices.iter().enumerate() {
        if i % 4096 == 0 {
            checkpoint()?;
        }
        base.extend_from_slice(&index.to_le_bytes());
    }
    if g.skin.is_none() && g.morph_targets.is_empty() {
        return Ok(base);
    }
    let mut bytes = Vec::new();
    bytes.extend_from_slice(b"FSG2");
    bytes.extend_from_slice(
        &u32::try_from(base.len())
            .map_err(|_| limited())?
            .to_le_bytes(),
    );
    bytes.extend_from_slice(&u32::from(g.skin.is_some()).to_le_bytes());
    bytes.extend_from_slice(
        &u32::try_from(g.morph_targets.len())
            .map_err(|_| limited())?
            .to_le_bytes(),
    );
    bytes.extend_from_slice(&base);
    if let Some(skin) = &g.skin {
        for (i, row) in skin.joints.iter().enumerate() {
            if i % 4096 == 0 {
                checkpoint()?;
            }
            for value in row {
                bytes.extend_from_slice(&value.to_le_bytes());
            }
        }
        for (i, row) in skin.weights.iter().enumerate() {
            if i % 4096 == 0 {
                checkpoint()?;
            }
            for value in row {
                bytes.extend_from_slice(&value.to_le_bytes());
            }
        }
    }
    for (index, target) in g.morph_targets.iter().enumerate() {
        if index % 4096 == 0 {
            checkpoint()?;
        }
        let name = target.name.as_bytes();
        bytes.extend_from_slice(
            &u32::try_from(name.len())
                .map_err(|_| limited())?
                .to_le_bytes(),
        );
        bytes.extend_from_slice(&u32::from(target.normals.is_some()).to_le_bytes());
        bytes.extend_from_slice(name);
        for (i, row) in target.positions.iter().enumerate() {
            if i % 4096 == 0 {
                checkpoint()?;
            }
            for value in row {
                bytes.extend_from_slice(&value.to_le_bytes());
            }
        }
        if let Some(normals) = &target.normals {
            for (i, row) in normals.iter().enumerate() {
                if i % 4096 == 0 {
                    checkpoint()?;
                }
                for value in row {
                    bytes.extend_from_slice(&value.to_le_bytes());
                }
            }
        }
    }
    Ok(bytes)
}

pub fn compact_geometry(bytes: &[u8]) -> Result<CompactedGeometry> {
    compaction_scratch_bytes(bytes)?;
    let input = geometry(bytes)?;
    let input_vertices = u32::try_from(input.positions.len()).map_err(|_| limited())?;
    let triangles = u32::try_from(input.indices.len() / 3).map_err(|_| limited())?;
    let output = remap_with_hash(&input, tuple_hash)?;
    let output_vertices = u32::try_from(output.positions.len()).map_err(|_| limited())?;
    let encoded = encode(&output)?;
    if encoded.len() > MAX_GEOMETRY_BYTES {
        return Err(limited());
    }
    checkpoint()?;
    geometry(&encoded)?;
    checkpoint()?;
    Ok(CompactedGeometry {
        bytes: encoded,
        input_vertices,
        output_vertices,
        triangles,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn fixture() -> Geometry {
        Geometry {
            positions: vec![
                [0., 0., 0.],
                [1., 0., 0.],
                [0., 1., 0.],
                [0., 0., 0.],
                [5., 5., 5.],
                [6., 6., 6.],
            ],
            normals: vec![[0., 0., 1.]; 6],
            tangents: None,
            uv: None,
            indices: vec![0, 1, 2, 3, 1, 2],
            skin: None,
            morph_targets: vec![],
        }
    }

    #[test]
    fn six_vertices_keep_both_faces_and_compact_idempotently() {
        let input = fixture();
        let bytes = encode(&input).unwrap();
        let first = compact_geometry(&bytes).unwrap();
        assert_eq!(
            (first.input_vertices, first.output_vertices, first.triangles),
            (6, 3, 2)
        );
        let result = geometry(&first.bytes).unwrap();
        assert_eq!(result.indices, [0, 1, 2, 0, 1, 2]);
        assert_eq!(result.positions, input.positions[..3]);
        assert_eq!(geometry(&bytes).unwrap().positions.len(), 6);
        let second = compact_geometry(&first.bytes).unwrap();
        assert_eq!(second.bytes, first.bytes);
        assert_eq!((second.input_vertices, second.output_vertices), (3, 3));
    }

    #[test]
    fn every_attribute_and_signed_zero_participates() {
        let base = fixture();
        for changed in 0..7 {
            let mut input = base.clone();
            input.uv = Some(vec![[0., 0.]; 6]);
            input.tangents = Some(vec![[1., 0., 0., 1.]; 6]);
            input.skin = Some(crate::VertexSkin {
                joints: vec![[0, 0, 0, 0]; 6],
                weights: vec![[1., 0., 0., 0.]; 6],
            });
            input.morph_targets.push(crate::MorphTarget {
                name: "expression".into(),
                positions: vec![[0., 0., 0.]; 6],
                normals: Some(vec![[0., 0., 0.]; 6]),
            });
            match changed {
                0 => input.normals[3] = [0., 1., 0.],
                1 => input.uv.as_mut().unwrap()[3][0] = 1.,
                2 => input.tangents.as_mut().unwrap()[3][3] = -1.,
                3 => input.skin.as_mut().unwrap().joints[3][1] = 1,
                4 => {
                    input.skin.as_mut().unwrap().joints[3][1] = 1;
                    input.skin.as_mut().unwrap().weights[3] = [0.5, 0.5, 0., 0.];
                }
                5 => input.morph_targets[0].positions[3][2] = 1.,
                6 => input.morph_targets[0].normals.as_mut().unwrap()[3][2] = 1.,
                _ => unreachable!(),
            }
            let result = compact_geometry(&encode(&input).unwrap()).unwrap();
            assert_eq!(result.output_vertices, 4, "changed attribute {changed}");
            assert_eq!(geometry(&result.bytes).unwrap().indices, [0, 1, 2, 3, 1, 2]);
        }
        let mut signed = base;
        signed.positions[3][0] = -0.0;
        let result = compact_geometry(&encode(&signed).unwrap()).unwrap();
        assert_eq!(result.output_vertices, 4);
        assert_eq!(
            geometry(&result.bytes).unwrap().positions[3][0].to_bits(),
            (-0.0_f32).to_bits()
        );
    }

    #[test]
    fn degenerate_face_is_not_removed_when_tuples_coalesce() {
        let mut input = fixture();
        input.indices = vec![0, 3, 0];
        let result = compact_geometry(&encode(&input).unwrap()).unwrap();
        assert_eq!(result.output_vertices, 1);
        assert_eq!(geometry(&result.bytes).unwrap().indices, [0, 0, 0]);
    }

    #[test]
    fn base_pose_collinear_face_can_open_under_morph() {
        let mut input = fixture();
        input.positions = vec![[0., 0., 0.], [1., 0., 0.], [2., 0., 0.]];
        input.normals = vec![[0., 0., 1.]; 3];
        input.indices = vec![0, 1, 2];
        input.morph_targets.push(crate::MorphTarget {
            name: "open".into(),
            positions: vec![[0., 0., 0.], [0., 1., 0.], [0., 0., 0.]],
            normals: None,
        });
        let result = geometry(&compact_geometry(&encode(&input).unwrap()).unwrap().bytes).unwrap();
        assert_eq!(result.indices, [0, 1, 2]);
        let raised_middle = result.positions[1][1] + result.morph_targets[0].positions[1][1];
        assert_eq!(raised_middle, 1.);
    }

    #[test]
    fn morph_normal_presence_and_target_name_survive() {
        let mut input = fixture();
        input.morph_targets.push(crate::MorphTarget {
            name: "without normals".into(),
            positions: vec![[0., 0., 0.]; 6],
            normals: None,
        });
        input.morph_targets.push(crate::MorphTarget {
            name: "with normals".into(),
            positions: vec![[0., 0., 0.]; 6],
            normals: Some(vec![[0., 0., 0.]; 6]),
        });
        let result = geometry(&compact_geometry(&encode(&input).unwrap()).unwrap().bytes).unwrap();
        assert_eq!(result.morph_targets[0].name, "without normals");
        assert!(result.morph_targets[0].normals.is_none());
        assert_eq!(result.morph_targets[1].name, "with normals");
        assert_eq!(result.morph_targets[1].normals.as_ref().unwrap().len(), 3);
    }

    #[test]
    fn cancelled_and_oversized_scratch_do_not_return_output() {
        use std::sync::{Arc, atomic::AtomicBool};
        let source = encode(&fixture()).unwrap();
        let cancelled = Arc::new(AtomicBool::new(true));
        let result = forge_tree_doc::cancellation::with_cancellation(cancelled, || {
            compact_geometry(&source)
        });
        assert!(result.is_err());
        let mut huge = vec![0; 16];
        huge[..4].copy_from_slice(b"FSG1");
        huge[8..12].copy_from_slice(&5_000_000_u32.to_le_bytes());
        assert!(compaction_scratch_bytes(&huge).is_err());
    }

    #[test]
    fn cancellation_is_polled_during_index_traversal() {
        use std::{
            cell::Cell,
            sync::{
                Arc,
                atomic::{AtomicBool, Ordering},
            },
        };
        let mut source = fixture();
        source.indices = vec![0; 8193];
        let flag = Arc::new(AtomicBool::new(false));
        let visited = Cell::new(0);
        let result = forge_tree_doc::cancellation::with_cancellation(flag.clone(), || {
            remap_with_hash(&source, |_, _| {
                let current = visited.get() + 1;
                visited.set(current);
                if current == 4097 {
                    flag.store(true, Ordering::Release);
                }
                0
            })
        });
        assert!(result.is_err());
        assert!(visited.get() <= 8193);
    }

    #[test]
    fn collisions_still_compare_complete_tuples() {
        let g = Geometry {
            positions: vec![[0., 0., 0.], [1., 0., 0.], [0., 1., 0.], [0., 0., 0.]],
            normals: vec![[0., 0., 1.]; 4],
            tangents: None,
            uv: None,
            indices: vec![0, 1, 2, 3, 1, 2],
            skin: None,
            morph_targets: vec![],
        };
        let compacted = remap_with_hash(&g, |_, _| 0).unwrap();
        assert_eq!(compacted.positions.len(), 3);
        assert_eq!(compacted.indices, [0, 1, 2, 0, 1, 2]);
    }
}
