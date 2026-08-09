# Aurora Agent Rules

@/home/phucle/.codex/RTK.md

## God View is the workflow Source of Truth

- Trước khi plan, diagnose, review hoặc sửa code, phải xác định workflow/domain liên quan và đọc đầy đủ God View tương ứng trong `god_view/`.
- God View là Source of Truth cho topology, ownership, state transition, transport, failure semantics, security boundary và recovery behavior của workflow.
- Không được chỉ đọc file đang sửa rồi suy luận workflow cục bộ. Phải trace end-to-end qua producer, transport, consumer, persistence và projection liên quan.
- Nếu code, config, contract và God View mâu thuẫn, không được âm thầm chọn code làm chuẩn. Phải nêu rõ discrepancy và reconcile về một contract thống nhất.
- Thay đổi làm đổi workflow contract phải cập nhật God View liên quan trong cùng change set.
- Nếu chưa có God View phù hợp, phải nói rõ khoảng trống Source of Truth. Với feature hoặc architecture change, tạo/cập nhật God View trước hoặc cùng lúc với implementation.

## Workflow-first development

- Đặt workflow end-to-end lên trước file, package, service hoặc abstraction.
- Trước khi implement, xác định rõ workflow owner, authority source, durable boundary, retry/settlement rule, failure mode và security invariant.
- Mỗi change chỉ nên tác động workflow được yêu cầu. Không kéo theo cleanup hoặc refactor của workflow khác.
- Ưu tiên implementation nằm trong module/workflow owner để giữ dependency direction và blast radius nhỏ.
- Test theo behavior và boundary của workflow: success, failure, retry/replay, stale event, authorization và recovery khi có liên quan.

## Workflow isolation over helpers

- Không tạo helper function theo mặc định.
- Giữ logic tại workflow owner và call site khi điều đó làm ownership, state transition và failure path rõ hơn.
- Chỉ tạo helper khi thật sự bắt buộc cho correctness hoặc security, hoặc khi không thể giữ invariant nhất quán tại call sites mà không tạo rủi ro thực tế.
- Helper bắt buộc phải có scope nhỏ nhất có thể: ưu tiên private function trong cùng workflow/module, sau đó mới tới package-local. Không đưa vào shared/global utility nếu chưa có nhiều workflow với cùng một contract được chứng minh.
- Không tạo generic abstraction, utility layer hoặc reusable helper để dự đoán nhu cầu tương lai.
- Không refactor code hiện có thành helper chỉ vì trùng cú pháp trong khi semantics, authority hoặc failure behavior thuộc các workflow khác nhau.
- Nếu buộc phải thêm helper, change description phải giải thích vì sao inline/workflow-local implementation không đủ và helper đó bảo toàn isolation như thế nào.

## Required working order

1. Tìm và đọc God View liên quan.
2. Trace workflow end-to-end trong code/config/contract hiện tại.
3. Chốt ownership, Source of Truth, invariants và failure semantics.
4. Implement thay đổi với scope workflow nhỏ nhất.
5. Verify bằng test/check đúng boundary.
6. Cập nhật God View nếu contract hoặc behavior thay đổi.

Các `AGENTS.md` sâu hơn trong cây thư mục bổ sung rule riêng cho subtree và phải được đọc trước khi làm việc trong subtree đó.
