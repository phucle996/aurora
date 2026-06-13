use std::collections::HashMap;

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
    }
    if *offset >= bytes.len() {
        return Err("Truncated null-terminated string".to_string());
    }
    let s = String::from_utf8_lossy(&bytes[start..*offset]).into_owned();
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
pub fn parse_insert_message(bytes: &[u8], columns: &[String]) -> Result<HashMap<String, String>, String> {
    let mut offset = 0;
    let tag = read_u8(bytes, &mut offset)?;
    if tag != b'I' {
        return Err("Not an insert message".to_string());
    }
    let _relation_id = read_u32(bytes, &mut offset)?;
    let tuple_tag = read_u8(bytes, &mut offset)?;
    if tuple_tag != b'N' {
        return Err(format!("Expected 'N' tuple tag, got '{}'", tuple_tag as char));
    }
    let num_columns = read_u16(bytes, &mut offset)?;
    if num_columns as usize != columns.len() {
        return Err(format!("Column count mismatch: relation has {}, insert has {}", columns.len(), num_columns));
    }
    
    let mut map = HashMap::new();
    for i in 0..num_columns as usize {
        let col_name = &columns[i];
        let val_type = read_u8(bytes, &mut offset)?;
        match val_type {
            // Trường hợp giá trị NULL
            b'n' => {
                map.insert(col_name.clone(), String::new());
            }
            // Trường hợp TOAST không đổi
            b'u' => {
                map.insert(col_name.clone(), String::new());
            }
            // Trường hợp giá trị dạng text thông thường
            b't' => {
                let len = read_u32(bytes, &mut offset)? as usize;
                if offset + len > bytes.len() {
                    return Err("Truncated tuple value".to_string());
                }
                let val = String::from_utf8_lossy(&bytes[offset..offset + len]).into_owned();
                offset += len;
                map.insert(col_name.clone(), val);
            }
            _ => {
                return Err(format!("Unknown tuple value type: {}", val_type as char));
            }
        }
    }
    
    Ok(map)
}
