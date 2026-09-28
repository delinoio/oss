//! Private bounded binary assets shared by the Node adapter and exporters.
use crate::*;

pub const MAX_MORPHS: usize = 64;

pub(crate) struct Reader<'a> {
    bytes: &'a [u8],
    offset: usize,
}
impl<'a> Reader<'a> {
    pub fn new(bytes: &'a [u8]) -> Result<Self> {
        if bytes.len() > MAX_GEOMETRY_BYTES {
            return Err(limited());
        }
        Ok(Self { bytes, offset: 0 })
    }

    pub fn take(&mut self, length: usize) -> Result<&'a [u8]> {
        let end = self.offset.checked_add(length).ok_or_else(limited)?;
        let out = self
            .bytes
            .get(self.offset..end)
            .ok_or_else(|| invalid("asset"))?;
        self.offset = end;
        Ok(out)
    }

    pub fn u32(&mut self) -> Result<u32> {
        Ok(u32::from_le_bytes(self.take(4)?.try_into().unwrap()))
    }

    pub fn floats<const N: usize>(&mut self, count: usize) -> Result<Vec<[f32; N]>> {
        let length = count
            .checked_mul(N)
            .and_then(|n| n.checked_mul(4))
            .ok_or_else(limited)?;
        let bytes = self.take(length)?;
        let mut out = Vec::with_capacity(count);
        for (i, chunk) in bytes.chunks_exact(N * 4).enumerate() {
            if i % 4096 == 0 {
                checkpoint()?;
            }
            let mut value = [0.; N];
            for (v, b) in value.iter_mut().zip(chunk.chunks_exact(4)) {
                *v = f32::from_le_bytes(b.try_into().unwrap());
                if !v.is_finite() {
                    return Err(invalid("asset"));
                }
            }
            out.push(value);
        }
        Ok(out)
    }

    pub fn finish(self) -> Result<()> {
        if self.offset != self.bytes.len() {
            return Err(invalid("asset"));
        }
        Ok(())
    }
}

#[derive(Clone, Debug)]
pub struct VertexSkin {
    pub joints: Vec<[u16; 4]>,
    pub weights: Vec<[f32; 4]>,
}
#[derive(Clone, Debug)]
pub struct MorphTarget {
    pub name: String,
    pub positions: Vec<[f32; 3]>,
    pub normals: Option<Vec<[f32; 3]>>,
}

/// FSG2 wraps a complete FSG1 payload, optional four-influence skin arrays,
/// and named dense morph deltas. Both envelopes are private worker transport.
pub(crate) fn extended_geometry(bytes: &[u8]) -> Result<Geometry> {
    let mut r = Reader::new(bytes)?;
    if r.take(4)? != b"FSG2" {
        return Err(invalid("geometry"));
    }
    let base_length = r.u32()? as usize;
    let flags = r.u32()?;
    let count = r.u32()? as usize;
    if flags > 1 || count > MAX_MORPHS {
        return Err(invalid("geometry"));
    }
    let base = r.take(base_length)?;
    // Do not permit recursive envelopes supplied directly to the native API.
    if !base.starts_with(b"FSG1") {
        return Err(invalid("geometry"));
    }
    let mut g = geometry(base)?;
    let n = g.positions.len();
    if flags == 1 {
        let raw = r.take(n.checked_mul(8).ok_or_else(limited)?)?;
        let mut joints = Vec::with_capacity(n);
        for (i, row) in raw.chunks_exact(8).enumerate() {
            if i % 4096 == 0 {
                checkpoint()?;
            }
            joints.push(std::array::from_fn(|j| {
                u16::from_le_bytes(row[j * 2..j * 2 + 2].try_into().unwrap())
            }));
        }
        let weights = r.floats::<4>(n)?;
        for (js, ws) in joints.iter().zip(&weights) {
            if ws.iter().any(|w| *w < 0. || *w > 1.) || (ws.iter().sum::<f32>() - 1.).abs() > 1e-6 {
                return Err(invalid("geometry/weights"));
            }
            for a in 0..4 {
                for b in a + 1..4 {
                    if ws[a] > 0. && ws[b] > 0. && js[a] == js[b] {
                        return Err(invalid("geometry/joints"));
                    }
                }
            }
        }
        g.skin = Some(VertexSkin { joints, weights });
    }
    let mut names = BTreeSet::new();
    for _ in 0..count {
        checkpoint()?;
        let length = r.u32()? as usize;
        let flags = r.u32()?;
        if length == 0 || length > 256 || flags > 1 {
            return Err(invalid("geometry/morph_targets"));
        }
        let name = std::str::from_utf8(r.take(length)?)
            .map_err(|_| invalid("geometry/morph_targets"))?
            .to_owned();
        if name.contains('\0') || !names.insert(name.clone()) {
            return Err(invalid("geometry/morph_targets"));
        }
        let positions = r.floats(n)?;
        let normals = if flags == 1 { Some(r.floats(n)?) } else { None };
        g.morph_targets.push(MorphTarget {
            name,
            positions,
            normals,
        });
    }
    r.finish()?;
    Ok(g)
}
