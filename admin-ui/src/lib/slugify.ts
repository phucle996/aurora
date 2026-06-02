/**
 * slugify.ts — Utility chuyển đổi string thành URL-safe slug.
 *
 * Hỗ trợ đầy đủ tiếng Việt và các ngôn ngữ có diacritics (Latin Extended).
 *
 * Algorithm:
 *   1. Map thủ công đ/Đ → d/D (NFD không decompose được ký tự này)
 *   2. NFD normalize — tách ký tự có dấu thành base char + combining diacritic
 *      ví dụ: 'à' → 'a' + U+0300, 'ộ' → 'o' + U+0302 + U+0323
 *   3. Strip toàn bộ combining diacritics (U+0300–U+036F)
 *   4. Lowercase
 *   5. Thay ký tự không phải alphanumeric/dash bằng '-'
 *   6. Trim dash ở đầu và cuối
 *
 * @example
 *   slugify('Hà Nội DC-1')      → 'ha-noi-dc-1'
 *   slugify('Đà Nẵng')          → 'da-nang'
 *   slugify('  Asia/Pacific  ') → 'asia-pacific'
 *   slugify('us-east-1')        → 'us-east-1'
 */
export function slugify(value: string): string {
  return value
    .trim()
    .replace(/[đĐ]/g, (c) => (c === 'đ' ? 'd' : 'D'))
    .normalize('NFD')
    .replace(/[\u0300-\u036f]/g, '')
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, '-')
    .replace(/^-+|-+$/g, '')
}
