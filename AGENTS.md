# Aurora Agent Rules

@/home/phucle/.codex/RTK.md

## God View is the workflow Source of Truth

- Trước khi plan, diagnose, review hoặc sửa code, phải xác định workflow/domain liên quan và đọc đầy đủ God View tương ứng trong `god_view/`.
- God View là Source of Truth cho topology, ownership, state transition, transport, failure semantics, security boundary và recovery behavior của workflow.
- Không được chỉ đọc file đang sửa rồi suy luận workflow cục bộ. Phải trace end-to-end qua producer, transport, consumer, persistence và projection liên quan.
- Nếu code, config, contract và God View mâu thuẫn, không được âm thầm chọn code làm chuẩn. Phải nêu rõ discrepancy và reconcile về một contract thống nhất.
- Thay đổi làm đổi workflow contract phải cập nhật God View liên quan trong cùng change set.
- Nếu chưa có God View phù hợp, phải nói rõ khoảng trống Source of Truth. Với feature hoặc architecture change, tạo/cập nhật God View trước hoặc cùng lúc với implementation.
- Nếu workflow HTTP đi qua ACR, God View phải bắt đầu bằng phase `Client → Envoy → ACR`. Phase đó bắt buộc nêu request method/path, headers và payload client gửi; `CheckRequest` Envoy đưa vào ACR; CORS/rate-limit/session/proof/path-rewrite/local state ACR xử lý; local response hoặc exact method/path/body và từng trusted header ACR remove/overwrite/inject sang upstream. Không được gộp ACR mơ hồ vào phase Client → Controlplane. Với ACR-local endpoint, phải nêu rõ không có upstream forward và response nào ACR trả qua Envoy.
- Trước các phase, God View HTTP phải có API-scope contract. `/personal` là platform-owned workflow: browser gọi neutral public route, ACR chọn/rewrite internal `/personal` từ verified session, Controlplane chạy permission authorization và required role level rồi repository rechecks durable facts. `/tenant` tương tự nhưng ACR rewrite `/tenant` từ verified tenant membership và authorization dùng tenant/workspace authority. `/me` là self-user workflow: target chỉ từ verified `x-user-id`, không owner rewrite, không `Authorize` permission/level middleware; critical `/me` thêm session-proof middleware nhưng không đổi thành owner route.
- Mỗi God View chỉ mô tả đúng một API workflow end-to-end và đúng một owner branch, với authority, transition và failure boundary riêng. Personal và tenant luôn là hai God View khác nhau dù browser path hoặc code implementation giống nhau. Không tạo God View cho authorization, RBAC catalog, cache hay một component đứng riêng: workflow nào dùng authorization phải mô tả authorizer, permission và required level ngay trong God View của workflow đó. Khi một God View cũ trộn nhiều API hoặc owner branch, tạo God View thay thế cho từng branch trước rồi mới bỏ file tổng hợp để không tạo khoảng trống SoT.
- Từ Phase 3 trở đi, tách theo owner và trigger thực tế thay vì ép một mẫu cố định: outbox/job, timeline, stream/Kafka consumer, projection, cleanup hoặc recovery đều là phase riêng khi có boundary khác. Mỗi phase phải chỉ rõ trigger, durable/retry/settlement rule và sequence qua component thật (producer, transport, consumer, service, repository/store); cấm vẽ participant tổng quát như `IAM`, `Job` hoặc `Timeline` làm hộp đen.

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

## Workflow contexts, never God Contexts

- Không dùng `#[allow(clippy::too_many_arguments)]` để unblock build hoặc che nợ thiết kế. Ngoại lệ phải được user phê duyệt tường minh trước khi commit.
- Khi workflow cần nhiều capability, tạo context riêng ngay trong module owner. Context chỉ được chứa capability mà workflow đó thật sự sử dụng.
- Dữ liệu nghiệp vụ, signed input và transport input phải đi qua command/request type có tên theo workflow; không trộn chúng vào capability context.
- Cấm `AppContext`, `ServiceContext`, dependency bag hoặc context dùng chung cho các workflow không cùng authority/failure boundary.
- Không di chuyển dependency vào context chỉ để giảm số argument. Mỗi field phải phản ánh capability, input hoặc invariant cụ thể của workflow owner.

## Required working order

1. Tìm và đọc God View liên quan.
2. Trace workflow end-to-end trong code/config/contract hiện tại.
3. Chốt ownership, Source of Truth, invariants và failure semantics.
4. Implement thay đổi với scope workflow nhỏ nhất.
5. Verify bằng test/check đúng boundary.
6. Cập nhật God View nếu contract hoặc behavior thay đổi.

Các `AGENTS.md` sâu hơn trong cây thư mục bổ sung rule riêng cho subtree và phải được đọc trước khi làm việc trong subtree đó.
