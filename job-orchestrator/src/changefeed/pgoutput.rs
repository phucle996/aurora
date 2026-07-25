use std::collections::HashMap;

const MAX_RELATION_COLUMNS: usize = 512;
const MAX_TEXT_VALUE_BYTES: usize = 1024 * 1024;

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ColumnValue {
    Null,
    UnchangedToast,
    Text(String),
}

#[derive(Debug, Clone)]
pub struct DecodedRow(HashMap<String, ColumnValue>);

impl DecodedRow {
    pub fn text(&self, column: &str) -> Option<&str> {
        match self.0.get(column) {
            Some(ColumnValue::Text(value)) => Some(value),
            Some(ColumnValue::Null | ColumnValue::UnchangedToast) | None => None,
        }
    }
}

/// Cấu trúc lưu trữ thông tin schema của bảng từ sự kiện Relation ('R') của pgoutput.
/// Điều này cần thiết để ánh xạ thứ tự cột nhị phân sang tên trường cụ thể.
#[derive(Debug, Clone)]
pub struct PgOutputRelation {
    pub relation_id: u32,
    pub schema_name: String,
    pub relation_name: String,
    pub columns: Vec<String>,
}

/// Hàm đọc u8 từ luồng byte kèm dịch chuyển offset
pub fn read_u8(bytes: &[u8], offset: &mut usize) -> Result<u8, String> {
    if *offset >= bytes.len() {
        return Err("Truncated u8".to_string());
    }
    let v = bytes[*offset];
    *offset += 1;
    Ok(v)
}

/// Hàm đọc u16 (big endian) từ luồng byte kèm dịch chuyển offset
pub fn read_u16(bytes: &[u8], offset: &mut usize) -> Result<u16, String> {
    if *offset + 2 > bytes.len() {
        return Err("Truncated u16".to_string());
    }
    let v = u16::from_be_bytes([bytes[*offset], bytes[*offset + 1]]);
    *offset += 2;
    Ok(v)
}

/// Hàm đọc u32 (big endian) từ luồng byte kèm dịch chuyển offset
pub fn read_u32(bytes: &[u8], offset: &mut usize) -> Result<u32, String> {
    if *offset + 4 > bytes.len() {
        return Err("Truncated u32".to_string());
    }
    let v = u32::from_be_bytes([
        bytes[*offset],
        bytes[*offset + 1],
        bytes[*offset + 2],
        bytes[*offset + 3],
    ]);
    *offset += 4;
    Ok(v)
}

/// Hàm đọc chuỗi null-terminated (kết thúc bằng byte 0) kèm dịch chuyển offset
pub fn read_string(bytes: &[u8], offset: &mut usize) -> Result<String, String> {
    let start = *offset;
    while *offset < bytes.len() && bytes[*offset] != 0 {
        *offset += 1;
        if *offset - start > 256 {
            return Err("pgoutput identifier exceeds decoder bound".to_string());
        }
    }
    if *offset >= bytes.len() {
        return Err("Truncated null-terminated string".to_string());
    }
    let s = std::str::from_utf8(&bytes[start..*offset])
        .map_err(|_| "Invalid UTF-8 in pgoutput identifier".to_string())?
        .to_string();
    *offset += 1; // Nhảy qua byte 0
    Ok(s)
}

/// Giải mã byte payload của Relation Message ('R') từ pgoutput
pub fn parse_relation_message(bytes: &[u8]) -> Result<PgOutputRelation, String> {
    let mut offset = 0;
    let tag = read_u8(bytes, &mut offset)?;
    if tag != b'R' {
        return Err("Not a relation message".to_string());
    }
    let relation_id = read_u32(bytes, &mut offset)?;
    let schema_name = read_string(bytes, &mut offset)?;
    let relation_name = read_string(bytes, &mut offset)?;
    let _replica_identity = read_u8(bytes, &mut offset)?;
    let num_columns = read_u16(bytes, &mut offset)?;
    if usize::from(num_columns) > MAX_RELATION_COLUMNS {
        return Err("Relation column count exceeds decoder bound".to_string());
    }

    let mut columns = Vec::with_capacity(num_columns as usize);
    for _ in 0..num_columns {
        let _flags = read_u8(bytes, &mut offset)?;
        let name = read_string(bytes, &mut offset)?;
        let _type_oid = read_u32(bytes, &mut offset)?;
        let _type_mod = read_u32(bytes, &mut offset)?;
        columns.push(name);
    }

    Ok(PgOutputRelation {
        relation_id,
        schema_name,
        relation_name,
        columns,
    })
}

/// Giải mã byte payload của Insert Message ('I') từ pgoutput dựa trên danh sách cột đã cache
pub fn parse_insert_message(bytes: &[u8], columns: &[String]) -> Result<DecodedRow, String> {
    let mut offset = 0;
    let tag = read_u8(bytes, &mut offset)?;
    if tag != b'I' {
        return Err("Not an insert message".to_string());
    }
    let _relation_id = read_u32(bytes, &mut offset)?;
    let tuple_tag = read_u8(bytes, &mut offset)?;
    if tuple_tag != b'N' {
        return Err(format!(
            "Expected 'N' tuple tag, got '{}'",
            tuple_tag as char
        ));
    }
    let num_columns = read_u16(bytes, &mut offset)?;
    if num_columns as usize != columns.len() {
        return Err(format!(
            "Column count mismatch: relation has {}, insert has {}",
            columns.len(),
            num_columns
        ));
    }

    let mut map = HashMap::with_capacity(usize::from(num_columns));
    for col_name in columns.iter().take(usize::from(num_columns)) {
        let val_type = read_u8(bytes, &mut offset)?;
        match val_type {
            // Trường hợp giá trị NULL
            b'n' => {
                map.insert(col_name.clone(), ColumnValue::Null);
            }
            // Trường hợp TOAST không đổi
            b'u' => {
                map.insert(col_name.clone(), ColumnValue::UnchangedToast);
            }
            // Trường hợp giá trị dạng text thông thường
            b't' => {
                let len = read_u32(bytes, &mut offset)? as usize;
                if len > MAX_TEXT_VALUE_BYTES {
                    return Err("Tuple value exceeds decoder bound".to_string());
                }
                if offset + len > bytes.len() {
                    return Err("Truncated tuple value".to_string());
                }
                let val = std::str::from_utf8(&bytes[offset..offset + len])
                    .map_err(|_| "Invalid UTF-8 in pgoutput text value".to_string())?
                    .to_string();
                offset += len;
                map.insert(col_name.clone(), ColumnValue::Text(val));
            }
            _ => {
                return Err(format!("Unknown tuple value type: {}", val_type as char));
            }
        }
    }

    Ok(DecodedRow(map))
}

/// Giải mã byte payload của Update Message ('U') từ pgoutput dựa trên danh sách cột đã cache
pub fn parse_update_message(bytes: &[u8], columns: &[String]) -> Result<DecodedRow, String> {
    let mut offset = 0;
    let tag = read_u8(bytes, &mut offset)?;
    if tag != b'U' {
        return Err("Not an update message".to_string());
    }
    let _relation_id = read_u32(bytes, &mut offset)?;

    // Đọc byte chỉ báo tuple tiếp theo
    let mut tuple_tag = read_u8(bytes, &mut offset)?;

    // Nếu có Old Tuple ('K' hoặc 'O'), ta cần nhảy qua (skip) nó
    if tuple_tag == b'K' || tuple_tag == b'O' {
        let num_old_cols = read_u16(bytes, &mut offset)?;
        if usize::from(num_old_cols) > MAX_RELATION_COLUMNS {
            return Err("Old tuple column count exceeds decoder bound".to_string());
        }
        for _ in 0..num_old_cols {
            let val_type = read_u8(bytes, &mut offset)?;
            match val_type {
                b'n' | b'u' => {}
                b't' => {
                    let len = read_u32(bytes, &mut offset)? as usize;
                    if len > MAX_TEXT_VALUE_BYTES {
                        return Err("Old tuple value exceeds decoder bound".to_string());
                    }
                    if offset + len > bytes.len() {
                        return Err("Truncated old tuple value".to_string());
                    }
                    offset += len;
                }
                _ => {
                    return Err(format!(
                        "Unknown old tuple value type: {}",
                        val_type as char
                    ));
                }
            }
        }
        // Đọc tiếp byte chỉ báo của New Tuple
        tuple_tag = read_u8(bytes, &mut offset)?;
    }

    if tuple_tag != b'N' {
        return Err(format!(
            "Expected 'N' tuple tag for update, got '{}'",
            tuple_tag as char
        ));
    }

    let num_columns = read_u16(bytes, &mut offset)?;
    if num_columns as usize != columns.len() {
        return Err(format!(
            "Column count mismatch: relation has {}, update has {}",
            columns.len(),
            num_columns
        ));
    }

    let mut map = HashMap::with_capacity(usize::from(num_columns));
    for col_name in columns.iter().take(usize::from(num_columns)) {
        let val_type = read_u8(bytes, &mut offset)?;
        match val_type {
            b'n' => {
                map.insert(col_name.clone(), ColumnValue::Null);
            }
            b'u' => {
                map.insert(col_name.clone(), ColumnValue::UnchangedToast);
            }
            b't' => {
                let len = read_u32(bytes, &mut offset)? as usize;
                if len > MAX_TEXT_VALUE_BYTES {
                    return Err("Tuple value exceeds decoder bound".to_string());
                }
                if offset + len > bytes.len() {
                    return Err("Truncated tuple value".to_string());
                }
                let val = std::str::from_utf8(&bytes[offset..offset + len])
                    .map_err(|_| "Invalid UTF-8 in pgoutput text value".to_string())?
                    .to_string();
                offset += len;
                map.insert(col_name.clone(), ColumnValue::Text(val));
            }
            _ => {
                return Err(format!("Unknown tuple value type: {}", val_type as char));
            }
        }
    }

    Ok(DecodedRow(map))
}

#[cfg(test)]
mod tests {
    use super::{parse_insert_message, ColumnValue};

    #[test]
    fn insert_preserves_null_toast_and_text_semantics() {
        let mut bytes = vec![b'I'];
        bytes.extend_from_slice(&1_u32.to_be_bytes());
        bytes.push(b'N');
        bytes.extend_from_slice(&3_u16.to_be_bytes());
        bytes.push(b'n');
        bytes.push(b'u');
        bytes.push(b't');
        bytes.extend_from_slice(&2_u32.to_be_bytes());
        bytes.extend_from_slice(b"ok");

        let row = parse_insert_message(
            &bytes,
            &[
                "nullable".to_string(),
                "toast".to_string(),
                "text".to_string(),
            ],
        )
        .unwrap();
        assert_eq!(row.0.get("nullable"), Some(&ColumnValue::Null));
        assert_eq!(row.0.get("toast"), Some(&ColumnValue::UnchangedToast));
        assert_eq!(row.text("text"), Some("ok"));
    }

    #[test]
    fn invalid_utf8_is_rejected_instead_of_lossily_rewritten() {
        let mut bytes = vec![b'I'];
        bytes.extend_from_slice(&1_u32.to_be_bytes());
        bytes.push(b'N');
        bytes.extend_from_slice(&1_u16.to_be_bytes());
        bytes.push(b't');
        bytes.extend_from_slice(&1_u32.to_be_bytes());
        bytes.push(0xff);

        assert!(parse_insert_message(&bytes, &["text".to_string()]).is_err());
    }
}
