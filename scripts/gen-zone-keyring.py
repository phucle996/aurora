#!/usr/bin/env python3
# ============================================================================
# 🔒 Zone Dataplane Keyring Generator
# ============================================================================
# Script tự động sinh tệp khoá giải mã Dataplane (job-payload-keys.json)
# chuẩn X25519 (Curve25519) + UUID v4 canonical cho môi trường Local Dev.
# 100% Pure Python Standard Library (không cần cài thêm pip package).
# ============================================================================

import os
import sys
import json
import uuid
import base64
from pathlib import Path

# [COMMENT]: Đường dẫn đĩa local tới tệp bí mật của Dataplane trong repo
SECRET_DIR = Path(__file__).resolve().parent.parent / "dataplane" / ".secrets"
KEY_FILE = SECRET_DIR / "job-payload-keys.json"

# [COMMENT]: Hằng số toán học Curve25519 theo tiêu chuẩn RFC 7748
P = 2**255 - 19
A24 = 121665

def clamp(n: int) -> int:
    # [COMMENT]: Clamping 32-byte scalar theo chuẩn X25519 (x25519_dalek/RFC 7748)
    n &= ~(7)
    n &= ~(128 << 248)
    n |= 64 << 248
    return n

def add(p, q, r):
    # [COMMENT]: Phép cộng điểm Montgomery trên đường cong Curve25519
    (X1, Z1) = r
    (X2, Z2) = p
    (X3, Z3) = q
    X4 = ((X2 * X3 - Z2 * Z3) ** 2) % P
    Z4 = (X1 * (X2 * Z3 - Z2 * X3) ** 2) % P
    return (X4, Z4)

def double(p):
    # [COMMENT]: Phép nhân đôi điểm (Point doubling) trên Curve25519
    (X1, Z1) = p
    X2 = ((X1 ** 2 - Z1 ** 2) ** 2) % P
    Z2 = (4 * X1 * Z1 * (X1 ** 2 + A24 * X1 * Z1 + Z1 ** 2)) % P
    return (X2, Z2)

def curve25519_eval(n: int, u: int = 9) -> int:
    # [COMMENT]: Nhân vô hướng X25519 Basepoint (u=9) trả về u-coordinate kết quả
    n = clamp(n)
    p0 = (1, 0)
    p1 = (u, 1)
    for i in range(254, -1, -1):
        bit = (n >> i) & 1
        if bit:
            p0, p1 = add(p0, p1, (u, 1)), double(p1)
        else:
            p0, p1 = double(p0), add(p0, p1, (u, 1))
    return (p0[0] * pow(p0[1], P - 2, P)) % P

def generate_x25519_keypair():
    # [COMMENT]: Sinh 32 random raw bytes bảo mật cho Private Key
    priv_bytes = os.urandom(32)
    scalar = int.from_bytes(priv_bytes, "little")
    
    # [COMMENT]: Tính toán Public Key tương ứng trên Curve25519
    pub_u = curve25519_eval(scalar, 9)
    pub_bytes = pub_u.to_bytes(32, "little")
    
    # [COMMENT]: Mã hóa Standard Padded Base64 theo đúng yêu cầu contract Rust Dataplane
    priv_b64 = base64.b64encode(priv_bytes).decode("utf-8")
    pub_b64 = base64.b64encode(pub_bytes).decode("utf-8")
    
    return priv_b64, pub_b64

def main():
    try:
        SECRET_DIR.mkdir(parents=True, exist_ok=True)
    except PermissionError:
        print(f"❌ Lỗi phân quyền khi tạo thư mục: {SECRET_DIR}")
        print(f"👉 Vui lòng đổi quyền sở hữu bằng lệnh: sudo chown -R $USER:$USER {SECRET_DIR}")
        sys.exit(1)
    
    if KEY_FILE.exists():
        print(f"✅ Tệp keyring đã tồn tại tại: {KEY_FILE}")
        try:
            with open(KEY_FILE, "r") as f:
                data = json.load(f)
            first_key = data.get("keys", [{}])[0]
            print(f"   Key ID: {first_key.get('key_id', 'N/A')}")
        except Exception:
            pass
        return

    # [COMMENT]: Sinh Key ID dạng UUID v4 canonical lowercase
    key_id = str(uuid.uuid4())
    priv_b64, pub_b64 = generate_x25519_keypair()

    keyring_data = {
        "keys": [
            {
                "key_id": key_id,
                "private_key": priv_b64
            }
        ]
    }

    # [COMMENT]: Ghi tệp JSON secret đĩa local để Dataplane mount Read-Only
    try:
        with open(KEY_FILE, "w") as f:
            json.dump(keyring_data, f, indent=2)
        os.chmod(KEY_FILE, 0o600)
    except PermissionError:
        print(f"❌ Lỗi phân quyền khi ghi tệp: {KEY_FILE}")
        print(f"👉 Vui lòng đổi quyền sở hữu bằng lệnh: sudo chown -R $USER:$USER {SECRET_DIR}")
        sys.exit(1)

    print("==============================================================")
    print("🎉 Đã tự động khởi tạo Zone Dataplane Keyring!")
    print("==============================================================")
    print(f"📁 Tệp Private Key : {KEY_FILE}")
    print(f"🔑 Key ID          : {key_id}")
    print(f"🔓 Public Key (B64): {pub_b64}")
    print("==============================================================")
    print("📌 Đăng ký Public Key trên Admin UI / Hierarchy API để kích hoạt!")
    print("==============================================================")

if __name__ == "__main__":
    main()
