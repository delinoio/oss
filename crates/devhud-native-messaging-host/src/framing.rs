use std::io::{self, Read, Write};

use serde::{Serialize, de::DeserializeOwned};

use crate::MAX_JSON_BYTES;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ByteOrder {
    LittleEndian,
    NativeEndian,
}

pub fn read_json<T: DeserializeOwned>(reader: &mut impl Read, order: ByteOrder) -> io::Result<T> {
    let mut prefix = [0_u8; 4];
    reader.read_exact(&mut prefix)?;
    let length = match order {
        ByteOrder::LittleEndian => u32::from_le_bytes(prefix),
        ByteOrder::NativeEndian => u32::from_ne_bytes(prefix),
    } as usize;
    if length > MAX_JSON_BYTES {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "message-too-large",
        ));
    }
    let mut body = vec![0_u8; length];
    reader.read_exact(&mut body)?;
    std::str::from_utf8(&body)
        .map_err(|_| io::Error::new(io::ErrorKind::InvalidData, "invalid-utf8"))?;
    serde_json::from_slice(&body)
        .map_err(|_| io::Error::new(io::ErrorKind::InvalidData, "invalid-json"))
}

pub fn write_json<T: Serialize>(
    writer: &mut impl Write,
    order: ByteOrder,
    value: &T,
) -> io::Result<()> {
    let body = serde_json::to_vec(value)
        .map_err(|_| io::Error::new(io::ErrorKind::InvalidData, "invalid-json"))?;
    if body.len() > MAX_JSON_BYTES {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "message-too-large",
        ));
    }
    let length = u32::try_from(body.len())
        .map_err(|_| io::Error::new(io::ErrorKind::InvalidData, "message-too-large"))?;
    let prefix = match order {
        ByteOrder::LittleEndian => length.to_le_bytes(),
        ByteOrder::NativeEndian => length.to_ne_bytes(),
    };
    writer.write_all(&prefix)?;
    writer.write_all(&body)?;
    writer.flush()
}

#[cfg(test)]
mod tests {
    use serde::{Deserialize, Serialize};

    use super::*;

    #[derive(Debug, Deserialize, Eq, PartialEq, Serialize)]
    struct Example {
        text: String,
    }

    #[test]
    fn frames_utf8_by_bytes() {
        let mut bytes = Vec::new();
        write_json(
            &mut bytes,
            ByteOrder::LittleEndian,
            &Example {
                text: "한글".into(),
            },
        )
        .unwrap();
        assert_eq!(
            u32::from_le_bytes(bytes[..4].try_into().unwrap()) as usize,
            bytes.len() - 4
        );
        assert_eq!(
            read_json::<Example>(&mut bytes.as_slice(), ByteOrder::LittleEndian).unwrap(),
            Example {
                text: "한글".into()
            }
        );
    }

    #[test]
    fn rejects_oversize_before_allocating_body() {
        let bytes = ((MAX_JSON_BYTES as u32) + 1).to_le_bytes();
        let error =
            read_json::<Example>(&mut bytes.as_slice(), ByteOrder::LittleEndian).unwrap_err();
        assert_eq!(error.to_string(), "message-too-large");
    }

    #[test]
    fn rejects_invalid_utf8_before_json_parsing() {
        let bytes = [1_u32.to_le_bytes().as_slice(), &[0xff]].concat();
        assert_eq!(
            read_json::<Example>(&mut bytes.as_slice(), ByteOrder::LittleEndian)
                .unwrap_err()
                .to_string(),
            "invalid-utf8"
        );
    }
}
