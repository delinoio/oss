//! Conservative endpoint-only simplification of a single static geometry asset.
use crate::*;

#[derive(Debug)]
pub struct SimplifiedGeometry {
    pub bytes: Vec<u8>,
    pub input_triangles: usize,
    pub output_triangles: usize,
    pub input_vertices: usize,
    pub output_vertices: usize,
    pub target_reached: bool,
    pub estimated_error: f64,
}

type Quadric = [f64; 16];
type Face = [u32; 3];

fn guarded(i: usize) -> Result<()> {
    if i.is_multiple_of(4096) {
        checkpoint()?;
    }
    Ok(())
}
fn edge(a: u32, b: u32) -> (u32, u32) {
    (a.min(b), a.max(b))
}
fn point(p: [f32; 3]) -> [f64; 3] {
    p.map(f64::from)
}
fn cross(a: [f64; 3], b: [f64; 3]) -> [f64; 3] {
    [
        a[1] * b[2] - a[2] * b[1],
        a[2] * b[0] - a[0] * b[2],
        a[0] * b[1] - a[1] * b[0],
    ]
}
fn sub(a: [f64; 3], b: [f64; 3]) -> [f64; 3] {
    [a[0] - b[0], a[1] - b[1], a[2] - b[2]]
}
fn dot(a: [f64; 3], b: [f64; 3]) -> f64 {
    a[0] * b[0] + a[1] * b[1] + a[2] * b[2]
}
fn normal(face: Face, positions: &[[f32; 3]]) -> [f64; 3] {
    let a = point(positions[face[0] as usize]);
    let b = point(positions[face[1] as usize]);
    let c = point(positions[face[2] as usize]);
    cross(sub(b, a), sub(c, a))
}
fn attribute_equal(g: &Geometry, a: usize, b: usize) -> bool {
    g.positions[a]
        .iter()
        .zip(g.positions[b])
        .all(|(x, y)| x.to_bits() == y.to_bits())
        && g.normals[a]
            .iter()
            .zip(g.normals[b])
            .all(|(x, y)| x.to_bits() == y.to_bits())
        && g.uv.as_ref().is_none_or(|v| {
            v[a].iter()
                .zip(v[b])
                .all(|(x, y)| x.to_bits() == y.to_bits())
        })
        && g.tangents.as_ref().is_none_or(|v| {
            v[a].iter()
                .zip(v[b])
                .all(|(x, y)| x.to_bits() == y.to_bits())
        })
}
fn squared_cost(q: &Quadric, p: [f32; 3], extent: f64) -> Result<f64> {
    let v = [f64::from(p[0]), f64::from(p[1]), f64::from(p[2]), 1.];
    let mut sum = 0.;
    for i in 0..4 {
        for j in 0..4 {
            sum += v[i] * q[i * 4 + j] * v[j];
        }
    }
    let cost = sum.max(0.) / (extent * extent);
    if cost.is_finite() {
        Ok(cost)
    } else {
        Err(limited())
    }
}
fn merged(a: &Quadric, b: &Quadric) -> Result<Quadric> {
    let mut out = [0.; 16];
    for i in 0..16 {
        out[i] = a[i] + b[i];
        if !out[i].is_finite() {
            return Err(limited());
        }
    }
    Ok(out)
}
fn edge_faces(faces: &[Face]) -> Result<BTreeMap<(u32, u32), Vec<usize>>> {
    let mut edges = BTreeMap::<(u32, u32), Vec<usize>>::new();
    for (i, f) in faces.iter().enumerate() {
        guarded(i)?;
        for (a, b) in [(f[0], f[1]), (f[1], f[2]), (f[2], f[0])] {
            // Index-degenerate source faces freeze their vertices and are never
            // repaired.
            if a != b {
                edges.entry(edge(a, b)).or_default().push(i);
            }
        }
    }
    Ok(edges)
}

fn vertex_faces(faces: &[Face]) -> Result<BTreeMap<u32, Vec<usize>>> {
    let mut vertices = BTreeMap::<u32, Vec<usize>>::new();
    for (i, face) in faces.iter().enumerate() {
        guarded(i)?;
        for &vertex in face {
            vertices.entry(vertex).or_default().push(i);
        }
    }
    Ok(vertices)
}

fn canonical_face(face: Face) -> Face {
    let mut key = face;
    key.sort_unstable();
    key
}

fn has_disconnected_fan(vertex: u32, faces: &[Face], incident: &[usize]) -> bool {
    let mut links = BTreeMap::<u32, BTreeSet<u32>>::new();
    for &index in incident {
        let face = faces[index];
        let neighbors: Vec<_> = face.into_iter().filter(|&id| id != vertex).collect();
        if neighbors.len() == 2 {
            links.entry(neighbors[0]).or_default().insert(neighbors[1]);
            links.entry(neighbors[1]).or_default().insert(neighbors[0]);
        }
    }
    let Some(&first) = links.keys().next() else {
        return false;
    };
    let mut visited = BTreeSet::from([first]);
    let mut pending = vec![first];
    while let Some(current) = pending.pop() {
        if let Some(neighbors) = links.get(&current) {
            for &neighbor in neighbors {
                if visited.insert(neighbor) {
                    pending.push(neighbor);
                }
            }
        }
    }
    visited.len() != links.len()
}

struct LegalityContext<'a> {
    faces: &'a [Face],
    edges: &'a BTreeMap<(u32, u32), Vec<usize>>,
    vertices: &'a BTreeMap<u32, Vec<usize>>,
    face_counts: &'a BTreeMap<Face, usize>,
    positions: &'a [[f32; 3]],
    target: usize,
}

fn legal(source: u32, destination: u32, context: &LegalityContext<'_>) -> Result<bool> {
    let faces = context.faces;
    let edges = context.edges;
    let vertices = context.vertices;
    let face_counts = context.face_counts;
    let positions = context.positions;
    let target = context.target;
    let incident = match edges.get(&edge(source, destination)) {
        Some(v) if v.len() <= 2 => v,
        _ => return Ok(false),
    };
    let mut link_source = BTreeSet::new();
    let mut link_destination = BTreeSet::new();
    let mut opposites = BTreeSet::new();
    for &i in vertices.get(&source).into_iter().flatten() {
        let f = faces[i];
        for v in f {
            if v != source {
                link_source.insert(v);
            }
        }
    }
    for &i in vertices.get(&destination).into_iter().flatten() {
        let f = faces[i];
        for v in f {
            if v != destination {
                link_destination.insert(v);
            }
        }
    }
    for &i in incident {
        let f = faces[i];
        for &v in &f {
            if v != source && v != destination {
                opposites.insert(v);
            }
        }
    }
    let common: BTreeSet<_> = link_source
        .intersection(&link_destination)
        .copied()
        .collect();
    if common != opposites {
        return Ok(false);
    }

    let mut removed = 0;
    for &i in vertices.get(&source).into_iter().flatten() {
        let f = faces[i];
        let next = f.map(|v| if v == source { destination } else { v });
        if next[0] == next[1] || next[1] == next[2] || next[2] == next[0] {
            if !f.contains(&destination) {
                return Ok(false);
            }
            removed += 1;
        } else {
            let before = normal(f, positions);
            let after = normal(next, positions);
            if !(dot(after, after) > 0. && dot(before, after) > 0.) {
                return Ok(false);
            }
        }
    }
    if faces.len() - removed < target || faces.len() - removed >= faces.len() {
        return Ok(false);
    }

    let mut affected_counts = BTreeMap::<Face, usize>::new();
    for &i in vertices.get(&source).into_iter().flatten() {
        *affected_counts.entry(canonical_face(faces[i])).or_default() += 1;
    }
    let mut new_counts = BTreeMap::<Face, usize>::new();
    for &i in vertices.get(&source).into_iter().flatten() {
        let face = faces[i];
        let next = face.map(|v| if v == source { destination } else { v });
        if next[0] == next[1] || next[1] == next[2] || next[2] == next[0] {
            continue;
        }
        let key = canonical_face(next);
        let existing = face_counts.get(&key).copied().unwrap_or(0)
            - affected_counts.get(&key).copied().unwrap_or(0);
        let created = new_counts.get(&key).copied().unwrap_or(0);
        if existing + created != 0 {
            return Ok(false);
        }
        *new_counts.entry(key).or_default() += 1;
    }
    Ok(true)
}

fn apply_collapse(
    source: u32,
    destination: u32,
    faces: &[Face],
    edges: &BTreeMap<(u32, u32), Vec<usize>>,
    target: usize,
) -> Result<Option<Vec<Face>>> {
    let mut output = Vec::with_capacity(faces.len());
    for (i, &f) in faces.iter().enumerate() {
        guarded(i)?;
        let mut next = f.map(|v| if v == source { destination } else { v });
        if next[0] == next[1] || next[1] == next[2] || next[2] == next[0] {
            continue;
        }
        output.push(std::mem::take(&mut next));
    }
    if output.len() < target || output.len() >= faces.len() {
        return Ok(None);
    }
    let next_edges = edge_faces(&output)?;
    if next_edges
        .iter()
        .any(|(e, uses)| uses.len() > 2 && edges.get(e).is_none_or(|old| old.len() <= 2))
    {
        return Ok(None);
    }
    Ok(Some(output))
}

/*
 * The candidate legality pass uses vertex incidence rather than rescanning
 * the complete face set for every candidate. The complete face rewrite
 * remains a single pass for the accepted candidate, preserving source face
 * order.
 */

/// A deliberately generous upper bound includes parsed tuples, quadrics, edge
/// maps, candidate scanning, a trial face set, remapping, and output encoding.
/// Refuse inputs above this bound before any operation-owned allocation.
pub fn simplification_scratch(bytes: &[u8]) -> Result<usize> {
    if bytes.len() > MAX_GEOMETRY_BYTES || bytes.len() < 16 {
        return Err(invalid("geometry"));
    }
    let (n, ni) = if bytes.starts_with(b"FSG1") {
        (8, 12)
    } else if bytes.starts_with(b"FSG2") {
        if bytes.len() < 32 || &bytes[16..20] != b"FSG1" {
            return Err(invalid("geometry"));
        }
        (24, 28)
    } else {
        return Err(invalid("geometry"));
    };
    let vertices = u32::from_le_bytes(bytes[n..n + 4].try_into().unwrap()) as usize;
    let indices = u32::from_le_bytes(bytes[ni..ni + 4].try_into().unwrap()) as usize;
    let bound = vertices
        .checked_mul(512)
        .and_then(|x| indices.checked_mul(384).and_then(|y| x.checked_add(y)))
        .and_then(|x| x.checked_add(bytes.len().checked_mul(4)?))
        .ok_or_else(limited)?;
    if bound > MAX_BYTES {
        Err(limited())
    } else {
        Ok(bound)
    }
}

pub fn simplify_geometry(
    bytes: &[u8],
    target: usize,
    max_error: f64,
) -> Result<SimplifiedGeometry> {
    simplification_scratch(bytes)?;
    let g = geometry(bytes)?;
    if g.skin.is_some() || !g.morph_targets.is_empty() {
        return Err(Diagnostic::new(
            ErrorCode::UnsupportedEdit,
            "geometry",
            "Skinned or morphed geometry cannot be simplified",
        ));
    }
    let input_triangles = g.indices.len() / 3;
    let input_vertices = g.positions.len();
    if target < 1 || target > input_triangles || !max_error.is_finite() || max_error < 0. {
        return Err(invalid("geometry"));
    }
    let unchanged = |reached| SimplifiedGeometry {
        bytes: bytes.to_vec(),
        input_triangles,
        output_triangles: input_triangles,
        input_vertices,
        output_vertices: input_vertices,
        target_reached: reached,
        estimated_error: 0.,
    };
    if target == input_triangles {
        return Ok(unchanged(true));
    }
    let mut bounds = [[f64::INFINITY; 3], [f64::NEG_INFINITY; 3]];
    for (i, &index) in g.indices.iter().enumerate() {
        guarded(i)?;
        let p = point(g.positions[index as usize]);
        for j in 0..3 {
            bounds[0][j] = bounds[0][j].min(p[j]);
            bounds[1][j] = bounds[1][j].max(p[j]);
        }
    }
    let extent = (0..3)
        .map(|j| bounds[1][j] - bounds[0][j])
        .fold(0., f64::max);
    if !extent.is_finite() {
        return Err(limited());
    }
    if extent == 0. {
        return Ok(unchanged(false));
    }
    let mut faces: Vec<Face> = g.indices.as_chunks::<3>().0.to_vec();
    let mut quadrics = vec![[0.; 16]; input_vertices];
    let mut frozen = vec![false; input_vertices];
    for (i, &f) in faces.iter().enumerate() {
        guarded(i)?;
        let n = normal(f, &g.positions);
        let length = dot(n, n).sqrt();
        if length == 0. || f[0] == f[1] || f[1] == f[2] || f[2] == f[0] {
            for &v in &f {
                frozen[v as usize] = true;
            }
            continue;
        }
        if !length.is_finite() {
            return Err(limited());
        }
        let p0 = point(g.positions[f[0] as usize]);
        let plane = [
            n[0] / length,
            n[1] / length,
            n[2] / length,
            -dot([n[0] / length, n[1] / length, n[2] / length], p0),
        ];
        if plane.iter().any(|x| !x.is_finite()) {
            return Err(limited());
        }
        for &v in &f {
            for a in 0..4 {
                for b in 0..4 {
                    quadrics[v as usize][a * 4 + b] += plane[a] * plane[b];
                }
            }
        }
    }
    if quadrics
        .iter()
        .flat_map(|q| q.iter())
        .any(|x| !x.is_finite())
    {
        return Err(limited());
    }
    let original_edges = edge_faces(&faces)?;
    for (&(a, b), uses) in &original_edges {
        if uses.len() != 2 {
            frozen[a as usize] = true;
            frozen[b as usize] = true;
        }
    }
    let original_vertices = vertex_faces(&faces)?;
    for (&vertex, incident) in &original_vertices {
        if has_disconnected_fan(vertex, &faces, incident) {
            frozen[vertex as usize] = true;
        }
    }
    let mut coincident = BTreeMap::<[u32; 3], Vec<usize>>::new();
    for (i, &p) in g.positions.iter().enumerate() {
        guarded(i)?;
        let key = p.map(|x| if x == 0. { 0 } else { x.to_bits() });
        coincident.entry(key).or_default().push(i);
    }
    for group in coincident.values() {
        if group.iter().any(|&id| !attribute_equal(&g, id, group[0])) {
            for &id in group {
                frozen[id] = true;
            }
        }
    }
    let mut maximum = 0f64;
    while faces.len() > target {
        checkpoint()?;
        let edges = edge_faces(&faces)?;
        let vertices = vertex_faces(&faces)?;
        let mut face_counts = BTreeMap::<Face, usize>::new();
        for (i, &face) in faces.iter().enumerate() {
            guarded(i)?;
            *face_counts.entry(canonical_face(face)).or_default() += 1;
        }
        let mut candidates = Vec::new();
        for (i, &(a, b)) in edges.keys().enumerate() {
            guarded(i)?;
            for (source, destination) in [(a, b), (b, a)] {
                if frozen[source as usize] {
                    continue;
                }
                let q = merged(&quadrics[source as usize], &quadrics[destination as usize])?;
                let cost = squared_cost(&q, g.positions[destination as usize], extent)?;
                if cost.sqrt() <= max_error {
                    candidates.push((cost, destination, source));
                }
            }
        }
        candidates.sort_by(|a, b| a.0.total_cmp(&b.0).then(a.1.cmp(&b.1)).then(a.2.cmp(&b.2)));
        let mut accepted = None;
        for (i, (cost, destination, source)) in candidates.into_iter().enumerate() {
            guarded(i)?;
            if legal(
                source,
                destination,
                &LegalityContext {
                    faces: &faces,
                    edges: &edges,
                    vertices: &vertices,
                    face_counts: &face_counts,
                    positions: &g.positions,
                    target,
                },
            )? && let Some(next) = apply_collapse(source, destination, &faces, &edges, target)?
            {
                accepted = Some((cost, destination, source, next));
                break;
            }
        }
        let Some((cost, destination, source, next)) = accepted else {
            break;
        };
        quadrics[destination as usize] =
            merged(&quadrics[source as usize], &quadrics[destination as usize])?;
        maximum = maximum.max(cost.sqrt());
        faces = next;
    }
    if faces.len() == input_triangles {
        return Ok(unchanged(false));
    }
    let mut remap = vec![u32::MAX; input_vertices];
    let mut order = Vec::<usize>::new();
    let output_indices = faces.len().checked_mul(3).ok_or_else(limited)?;
    let mut indices = Vec::<u32>::with_capacity(output_indices);
    for (i, f) in faces.iter().enumerate() {
        guarded(i)?;
        for &v in f {
            let slot = &mut remap[v as usize];
            if *slot == u32::MAX {
                *slot = u32::try_from(order.len()).map_err(|_| limited())?;
                order.push(v as usize);
            }
            indices.push(*slot);
        }
    }
    let flags = u32::from(g.uv.is_some()) | (u32::from(g.tangents.is_some()) << 1);
    let stride =
        24 + if g.tangents.is_some() { 16 } else { 0 } + if g.uv.is_some() { 8 } else { 0 };
    let length = 16usize
        .checked_add(order.len().checked_mul(stride).ok_or_else(limited)?)
        .and_then(|n| n.checked_add(indices.len().checked_mul(4)?))
        .ok_or_else(limited)?;
    if length > MAX_GEOMETRY_BYTES {
        return Err(limited());
    }
    let mut output = Vec::with_capacity(length);
    output.extend_from_slice(b"FSG1");
    output.extend_from_slice(&flags.to_le_bytes());
    output.extend_from_slice(
        &u32::try_from(order.len())
            .map_err(|_| limited())?
            .to_le_bytes(),
    );
    output.extend_from_slice(
        &u32::try_from(indices.len())
            .map_err(|_| limited())?
            .to_le_bytes(),
    );
    for (i, &id) in order.iter().enumerate() {
        guarded(i)?;
        for v in g.positions[id] {
            output.extend_from_slice(&v.to_le_bytes());
        }
    }
    for (i, &id) in order.iter().enumerate() {
        guarded(i)?;
        for v in g.normals[id] {
            output.extend_from_slice(&v.to_le_bytes());
        }
    }
    if let Some(ref tangents) = g.tangents {
        for (i, &id) in order.iter().enumerate() {
            guarded(i)?;
            for v in tangents[id] {
                output.extend_from_slice(&v.to_le_bytes());
            }
        }
    }
    if let Some(ref uv) = g.uv {
        for (i, &id) in order.iter().enumerate() {
            guarded(i)?;
            for v in uv[id] {
                output.extend_from_slice(&v.to_le_bytes());
            }
        }
    }
    for (i, v) in indices.into_iter().enumerate() {
        guarded(i)?;
        output.extend_from_slice(&v.to_le_bytes());
    }
    geometry(&output)?;
    Ok(SimplifiedGeometry {
        bytes: output,
        input_triangles,
        output_triangles: faces.len(),
        input_vertices,
        output_vertices: order.len(),
        target_reached: faces.len() == target,
        estimated_error: maximum,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    fn encode(positions: &[[f32; 3]], normals: &[[f32; 3]], indices: &[u32]) -> Vec<u8> {
        let mut bytes = b"FSG1".to_vec();
        bytes.extend_from_slice(&0u32.to_le_bytes());
        bytes.extend_from_slice(&(positions.len() as u32).to_le_bytes());
        bytes.extend_from_slice(&(indices.len() as u32).to_le_bytes());
        for row in positions {
            for value in row {
                bytes.extend_from_slice(&value.to_le_bytes());
            }
        }
        for row in normals {
            for value in row {
                bytes.extend_from_slice(&value.to_le_bytes());
            }
        }
        for index in indices {
            bytes.extend_from_slice(&index.to_le_bytes());
        }
        bytes
    }
    fn grid() -> Vec<u8> {
        let positions: Vec<_> = (0..3)
            .flat_map(|y| (0..3).map(move |x| [x as f32 / 2., y as f32 / 2., 0.]))
            .collect();
        let normals = vec![[0., 0., 1.]; 9];
        let mut indices = Vec::new();
        for y in 0..2 {
            for x in 0..2 {
                let a = y * 3 + x;
                let b = a + 1;
                let c = a + 3;
                let d = c + 1;
                indices.extend_from_slice(&[a, b, d, a, d, c]);
            }
        }
        encode(&positions, &normals, &indices)
    }
    #[test]
    fn planar_grid_stops_at_boundary_without_changing_source() {
        let source = grid();
        let six = simplify_geometry(&source, 6, 0.).unwrap();
        assert_eq!(
            (
                six.input_triangles,
                six.output_triangles,
                six.output_vertices
            ),
            (8, 6, 8)
        );
        assert!(six.target_reached);
        assert_eq!(six.estimated_error, 0.);
        assert_eq!(
            simplify_geometry(&source, 4, 0.).unwrap().output_triangles,
            6
        );
        assert!(!simplify_geometry(&source, 4, 0.).unwrap().target_reached);
        assert_eq!(simplify_geometry(&source, 8, 0.).unwrap().bytes, source);
        assert_eq!(simplify_geometry(&source, 6, 0.).unwrap().bytes, six.bytes);
        let result = geometry(&six.bytes).unwrap();
        assert_eq!(result.positions.len(), 8);
        for p in result.positions {
            assert!(p[0] == 0. || p[0] == 1. || p[1] == 0. || p[1] == 1.);
        }
    }
    #[test]
    fn disconnected_patches_keep_their_own_boundary_vertices() {
        let first = geometry(&grid()).unwrap();
        let mut positions = first.positions.clone();
        positions.extend(first.positions.iter().map(|p| [p[0] + 2., p[1], p[2]]));
        let normals = vec![[0., 0., 1.]; 18];
        let mut indices = first.indices.clone();
        indices.extend(first.indices.iter().map(|i| i + 9));
        let result = simplify_geometry(&encode(&positions, &normals, &indices), 12, 0.).unwrap();
        assert_eq!((result.output_triangles, result.output_vertices), (12, 16));
        let output = geometry(&result.bytes).unwrap();
        assert!(output.indices.as_chunks::<3>().0.iter().all(|f| {
            let x = output.positions[f[0] as usize][0];
            f.iter()
                .all(|&v| (output.positions[v as usize][0] < 1.5) == (x < 1.5))
        }));
    }
    #[test]
    fn separately_indexed_cube_faces_keep_sharp_tuples() {
        let faces = [
            (
                [[-1., -1., 1.], [1., -1., 1.], [1., 1., 1.], [-1., 1., 1.]],
                [0., 0., 1.],
            ),
            (
                [
                    [1., -1., -1.],
                    [-1., -1., -1.],
                    [-1., 1., -1.],
                    [1., 1., -1.],
                ],
                [0., 0., -1.],
            ),
            (
                [[1., -1., 1.], [1., -1., -1.], [1., 1., -1.], [1., 1., 1.]],
                [1., 0., 0.],
            ),
            (
                [
                    [-1., -1., -1.],
                    [-1., -1., 1.],
                    [-1., 1., 1.],
                    [-1., 1., -1.],
                ],
                [-1., 0., 0.],
            ),
            (
                [[-1., 1., 1.], [1., 1., 1.], [1., 1., -1.], [-1., 1., -1.]],
                [0., 1., 0.],
            ),
            (
                [
                    [-1., -1., -1.],
                    [1., -1., -1.],
                    [1., -1., 1.],
                    [-1., -1., 1.],
                ],
                [0., -1., 0.],
            ),
        ];
        let mut positions = Vec::new();
        let mut normals = Vec::new();
        let mut indices = Vec::new();
        for (quad, normal) in faces {
            let base = positions.len() as u32;
            positions.extend(quad);
            normals.extend([normal; 4]);
            indices.extend([base, base + 1, base + 2, base, base + 2, base + 3]);
        }
        let source = encode(&positions, &normals, &indices);
        let result = simplify_geometry(&source, 6, 10.).unwrap();
        assert_eq!((result.output_triangles, result.output_vertices), (12, 24));
        assert!(!result.target_reached);
        assert_eq!(result.bytes, source);
    }
    #[test]
    fn signed_zero_coincidence_freezes_uv_and_handedness_seams() {
        let source = geometry(&grid()).unwrap();
        let mut positions = source.positions;
        positions.push([0.5, 0.5, -0.0]);
        let mut normals = source.normals;
        normals.push([0., 0., 1.]);
        let mut bytes = b"FSG1".to_vec();
        bytes.extend_from_slice(&3u32.to_le_bytes());
        bytes.extend_from_slice(&10u32.to_le_bytes());
        bytes.extend_from_slice(&(source.indices.len() as u32).to_le_bytes());
        for row in &positions {
            for value in row {
                bytes.extend_from_slice(&value.to_le_bytes());
            }
        }
        for row in &normals {
            for value in row {
                bytes.extend_from_slice(&value.to_le_bytes());
            }
        }
        for i in 0..10 {
            for value in [1f32, 0., 0., if i == 9 { -1. } else { 1. }] {
                bytes.extend_from_slice(&value.to_le_bytes());
            }
        }
        for i in 0..10 {
            for value in [if i == 9 { 1f32 } else { 0. }, 0.] {
                bytes.extend_from_slice(&value.to_le_bytes());
            }
        }
        for index in source.indices {
            bytes.extend_from_slice(&index.to_le_bytes());
        }
        let result = simplify_geometry(&bytes, 6, 0.).unwrap();
        assert_eq!(result.output_triangles, 8);
        assert!(!result.target_reached);
        assert_eq!(result.bytes, bytes);
    }
    #[test]
    fn nonmanifold_and_degenerate_incidents_remain_conservative() {
        let positions = [
            [0., 0., 0.],
            [1., 0., 0.],
            [0., 1., 0.],
            [0., -1., 0.],
            [0., 0., 1.],
        ];
        let normals = [[0., 0., 1.]; 5];
        let indices = [0, 1, 2, 1, 0, 3, 0, 1, 4, 2, 2, 3];
        let source = encode(&positions, &normals, &indices);
        let result = simplify_geometry(&source, 1, 10.).unwrap();
        assert_eq!(result.output_triangles, 4);
        assert!(!result.target_reached);
        assert_eq!(result.bytes, source);
    }
    #[test]
    fn tetrahedron_collapses_do_not_create_duplicate_faces() {
        let positions = [[0., 0., 0.], [1., 0., 0.], [0., 1., 0.], [0., 0., 1.]];
        let normals = [[0., 0., 1.]; 4];
        let indices = [0, 2, 1, 0, 1, 3, 0, 3, 2, 1, 2, 3];
        let source = encode(&positions, &normals, &indices);
        let result = simplify_geometry(&source, 1, 10.).unwrap();
        assert_eq!(result.output_triangles, 4);
        assert!(!result.target_reached);
        assert_eq!(result.bytes, source);
    }

    #[test]
    fn disconnected_vertex_fans_are_frozen() {
        let positions = [
            [0., 0., 0.],
            [1., 0., 0.],
            [0., 1., 0.],
            [0., 0., 1.],
            [-1., 0., 0.],
            [0., -1., 0.],
            [0., 0., -1.],
        ];
        let normals = [[0., 0., 1.]; 7];
        let indices = [
            0, 2, 1, 0, 1, 3, 0, 3, 2, 1, 2, 3, 0, 4, 5, 0, 5, 6, 0, 6, 4, 4, 6, 5,
        ];
        let source = encode(&positions, &normals, &indices);
        let result = simplify_geometry(&source, 1, 10.).unwrap();
        let output = geometry(&result.bytes).unwrap();
        assert!(output.positions.iter().any(|p| p == &[0., 0., 0.]));
    }
    #[test]
    fn tied_candidates_use_original_destination_then_source_ids() {
        let mut candidates: [(f64, u32, u32); 4] =
            [(0.25, 7, 3), (0.25, 2, 8), (0.25, 2, 4), (0.1, 9, 1)];
        candidates.sort_by(|a, b| a.0.total_cmp(&b.0).then(a.1.cmp(&b.1)).then(a.2.cmp(&b.2)));
        assert_eq!(
            candidates,
            [(0.1, 9, 1), (0.25, 2, 4), (0.25, 2, 8), (0.25, 7, 3)]
        );
    }
    #[test]
    fn metric_gate_is_exact() {
        let mut a = [0.; 16];
        a[0] = 1.;
        a[10] = 1.;
        let mut b = [0.; 16];
        b[10] = 1.;
        let q = merged(&a, &b).unwrap();
        assert_eq!(squared_cost(&q, [1., 0., 0.], 2.).unwrap(), 0.25);
        assert!(squared_cost(&q, [1., 0., 0.], 2.).unwrap().sqrt() <= 0.5);
        assert!(squared_cost(&q, [1., 0., 0.], 2.).unwrap().sqrt() > 0.49);
        assert_eq!(0.25_f64.max(0.5), 0.5);
    }
    #[test]
    fn scratch_preflight_rejects_excess_before_geometry_parse() {
        let mut header = b"FSG1".to_vec();
        header.extend_from_slice(&0u32.to_le_bytes());
        header.extend_from_slice(&200_000u32.to_le_bytes());
        header.extend_from_slice(&600_000u32.to_le_bytes());
        let error = simplification_scratch(&header).unwrap_err();
        assert_eq!(error.code, ErrorCode::ResourceLimit);
    }
}
