# Aurora Cloud Console UI Design Guidelines

Tài liệu này đặc tả chi tiết phong cách thiết kế giao diện (Design System) của **Aurora Cloud Console**. 

Mục tiêu là định hình giao diện từ một **SaaS Dashboard** thông thường sang **Enterprise Cloud Control Plane** chuyên nghiệp dành cho người vận hành hạ tầng, lấy cảm hứng từ sự nhất quán của **Azure Portal (70%)**, sự tối giản dữ liệu của **AWS Console (20%)**, và cấu trúc hiện đại của **GCP Console (10%)**.

---

## 1. Triết lý thiết kế tổng quan (Design Philosophy)

* **Tập trung vào Dữ liệu (Data-Centric)**: Màu sắc và hiệu ứng hình ảnh đóng vai trò phụ trợ. Dữ liệu và trạng thái hệ thống phải là thứ nổi bật nhất.
* **Tối ưu hóa thời gian làm việc (High Endurance UI)**: Giao diện sử dụng tông màu dịu, giảm độ tương phản chói và độ lấp lánh (gradient, shadow lớn) để người vận hành có thể làm việc liên tục nhiều giờ mà không mỏi mắt.
* **Ưu tiên Border hơn Shadow (Border First)**: Không lạm dụng bóng đổ để phân tách các thành phần. Sử dụng các đường viền mỏng (`border: 1px`) kết hợp màu nền khác biệt nhẹ để phân tầng giao diện.

---

## 2. Design Tokens cơ bản

### 2.1. Bo góc (Border Radius)
Tuyệt đối không sử dụng bo góc lớn (12px – 20px) cho các thành phần chính vì tạo cảm giác CRM/SaaS không chuyên nghiệp.

| Component | Radius Token | Lớp Tailwind tương ứng |
| :--- | :---: | :--- |
| **Card / Surface** | 4px | `rounded-[4px]` |
| **Button** | 4px | `rounded-[4px]` |
| **Input / Search** | 4px | `rounded-[4px]` |
| **Dropdown / Menu** | 4px | `rounded-[4px]` |
| **Table Grid** | 0 – 4px | `rounded-none` hoặc `rounded-[4px]` |
| **Modal / Dialog** | 6px | `rounded-[6px]` |

### 2.2. Khoảng cách & Spacing (Information Density)
* Giảm padding và margin từ 20–30% so với dashboard thông thường.
* Tiêu đề và mô tả đặt gần nhau hơn (`space-y-0.5` hoặc `gap-1`).
* Toolbar lọc và tìm kiếm được thiết kế mỏng gọn (`h-9` hoặc padding `py-2 px-3`).

### 2.3. Bóng đổ (Shadows)
* Tránh sử dụng `shadow-md`, `shadow-lg` cho các khối chính.
* Chỉ sử dụng bóng đổ rất nhẹ (`shadow-sm` hoặc `shadow-xs`) cho các thành phần nổi hẳn lên trên bề mặt như: **Dropdown Menu**, **Context Menu**, và **Dialog**.

### 2.4. Hệ thống phân cấp Chữ & Tương phản (Typography Scale & Contrast)
Nhằm duy trì khả năng đọc dữ liệu lâu dài và hiển thị lượng thông tin dày đặc, Aurora áp dụng hệ thống phân cấp chữ có độ tương phản và độ đậm (weight) rõ ràng:

* **Bảng phân cấp cỡ chữ (Typography Scale)**:
  * **Page Title**: `24px` / Weight 600 (`text-2xl font-semibold` / Màu Text Primary)
  * **Section Title**: `18px` / Weight 600 (`text-lg font-semibold`)
  * **Card Title**: `15px` / Weight 600 (`text-[15px] font-semibold`)
  * **Body / Sidebar / Table**: `13px` / Weight 400 hoặc 500 (`text-[13px] font-normal/font-medium`)
  * **Table Header / Captions**: `12px` / Weight 600 (`text-[12px] font-semibold`)
  * **Badge / Small Labels**: `11px` / Weight 600 (`text-[11px] font-semibold`)
  * *Lưu ý*: Đối với các trường hiển thị quan trọng trong bảng (như Tên tổ chức, Tên VM), áp dụng cỡ chữ **14px / Weight 600** (`text-sm font-semibold`) để làm điểm nhấn thị giác.
* **Độ tương phản màu chữ (Text Contrast)**:
  * **Chữ chính (Primary Text)**: Phải sử dụng màu chữ có độ tương phản cao gần như tuyệt đối (`#111827` cho Light và `#F3F4F6` cho Dark).
  * **Chữ phụ / Mô tả (Secondary/Muted Text)**: Sử dụng màu xám trung tính (`#6B7280` cho Light và `#A1A1AA` cho Dark) để làm mờ nhẹ các thông tin metadata, mô tả phụ trợ.

---

## 3. Hệ thống Màu sắc (Color System)

Màu sắc trong console chỉ được dùng để biểu thị: **Trạng thái (Status)**, **Hành động chính (Primary Action)**, **Cảnh báo (Warning)** và **Điều hướng được chọn (Selected)**. Khoảng **85–90%** giao diện sử dụng bảng màu trung tính (Neutral Palette).

### 3.1. Neutral Palette
* **Light Theme (Sáng dịu mắt)**:
  * Nền ứng dụng chính (App Background): `#F5F7FA` (Xám nhạt dịu, không dùng trắng tinh `#FFFFFF` để tránh chói).
  * Nền trang và các ô Surface (Page/Card Background): `#FFFFFF`.
  * Đường viền (Border / Input border): `#E5E7EB`.
  * Chữ chính (Text Primary): `#111827`.
  * Chữ phụ (Text Secondary): `#6B7280`.
* **Dark Theme (Xám than - Charcoal)**:
  * Nền ứng dụng chính (App Background): `#111315` (Không dùng đen tuyệt đối `#000000`).
  * Nền ô chứa (Surface / Cards): `#1A1D21`.
  * Bề mặt nổi (Elevated Surface / Modals / Popovers): `#23272F`.
  * Đường viền (Border): `#30343A`.
  * Chữ chính (Text Primary): `#F3F4F6`.
  * Chữ phụ (Text Secondary): `#A1A1AA`.

### 3.2. Status Colors (Bảng màu trạng thái hệ thống)

Màu trạng thái phải được định nghĩa nhất quán trên toàn bộ hệ thống:

| Trạng thái (Status) | Màu sắc (Color) | Mã màu Tailwind | Ý nghĩa |
| :--- | :---: | :---: | :--- |
| **Healthy / Operational** | Green / Emerald | `text-emerald-500` / `bg-emerald-500` | Hoạt động bình thường, ổn định. |
| **Warning / Degraded** | Amber | `text-amber-500` / `bg-amber-500` | Hiệu năng giảm, cần lưu ý. |
| **Error / Outage** | Red | `text-red-500` / `bg-red-500` | Gặp sự cố nghiêm trọng, dừng hoạt động. |
| **Info / Updating** | Blue | `text-blue-500` / `bg-blue-500` | Đang cập nhật, cấu hình thông tin. |
| **Disabled / Offline** | Gray | `text-slate-400` / `bg-slate-400` | Vô hiệu hóa, không hoạt động. |

---

## 4. Cấu trúc phân tầng bề mặt (Surface Hierarchy)

Giao diện Cloud Console được phân tách các lớp rõ ràng bằng cách thay đổi độ sáng của nền từ 2-4%, hạn chế phụ thuộc vào bóng đổ:

```
[ Lớp 1: App Background ] (#F5F7FA / #111315)
        ↓
[ Lớp 2: Page Content / Table Container ] (#FFFFFF / #1A1D21)
        ↓
[ Lớp 3: Card / Inner Widgets ] (#FFFFFF / #1A1D21)
        ↓
[ Lớp 4: Dropdown / Dialog / Modal ] (#FFFFFF / #23272F)
```

---

## 5. Sidebar & Navigation Menu

Thanh điều hướng bên (Sidebar) được thiết kế theo phong cách tối giản của Azure Portal:

* **Chiều cao item**: Rút gọn xuống `h-8` thay vì `h-10` để hiển thị nhiều menu dịch vụ hơn.
* **Cỡ chữ**: Dùng `text-xs font-medium` để hiển thị nhãn ngắn gọn.
* **Active State**: Làm nổi bật rõ nét bằng đường border trái mỏng (`3px`) màu xanh thương hiệu (`#3B82F6`) và màu nền mờ phủ nhẹ (`bg-sidebar-console-active-bg`).
* **Hover State**: Thay đổi màu nền rất nhẹ (`hover:bg-sidebar-console-hover`).
* **Sidebar Token**:
  * **Light Sidebar Background**: `#111827` (Gray 900)
  * **Dark Sidebar Background**: `#181A1E`

---

## 6. Thiết kế ưu tiên Bảng dữ liệu (Table-First Design)

Đối với các màn hình quản trị tài nguyên hạ tầng (Virtual Machines, Kubernetes, Storage, Tenants/Orgs, IAM, v.v.), **không lạm dụng dạng lưới Card View**.

* **Mặc định**: Luôn hiển thị dữ liệu dưới dạng **Bảng (Table) hoặc Data Grid**.
* **Card View**: Chỉ cung cấp dưới dạng tùy chọn phụ (Toggle View Mode) hoặc dành cho trang Overview tổng quan.
* **Cấu trúc bảng**:
  * Đường phân chia hàng mỏng nhẹ (`divide-border/40`).
  * Padding ô dữ liệu hẹp (`py-2 px-3`) để tăng mật độ thông tin.
  * Tên tài nguyên (ví dụ: VM Name, Tenant Name) luôn được in đậm rõ nét.

---

## 7. Khả năng tiếp cận & Trạng thái tương tác (Accessibility & States)

### 7.1. Tiêu chuẩn tiếp cận WCAG AA
* **Độ tương phản**: Độ tương phản chữ chính trên nền tối thiểu đạt tỷ lệ **4.5:1** để có thể đọc rõ ràng trong môi trường ánh sáng yếu.
* **Không dùng màu sắc đơn độc để báo hiệu trạng thái**: 
  * Không bao giờ chỉ dùng một dấu chấm xanh hoặc đỏ để chỉ trạng thái. 
  * Luôn đi kèm với biểu tượng chỉ dẫn và văn bản cụ thể.
  * *Ví dụ*: Hiển thị là `✔ Healthy` hoặc `⚠ Warning` hoặc `✖ Failed`.

### 7.2. Đầy đủ các Interactive States
Mọi thành phần tương tác (Button, Input, Menu Item) đều phải định nghĩa rõ các lớp trạng thái:
* **Default**: Trạng thái hiển thị ban đầu.
* **Hover**: Phản hồi khi di chuột qua (tăng nhẹ độ sáng hoặc đổi nền mờ).
* **Pressed / Active**: Phản hồi khi click chuột xuống.
* **Focused**: Đường ring bao quanh khi duyệt bằng phím Tab (`focus-visible:ring`).
* **Disabled**: Mờ đi (opacity 50%) và ngắt toàn bộ sự kiện chuột.

---

## 8. Quy tắc bố cục co giãn & Thích ứng (Responsive & Fluid Layout Rule)

Để tối ưu hóa không gian làm việc trên màn hình lớn khi đóng/mở Sidebar, Aurora áp dụng cơ chế bố cục co giãn toàn diện (Fluid Layout):

* **Bố cục co giãn động (Fluid Width)**: 
  * Loại bỏ giới hạn chiều rộng cứng (`max-width: 1200px` hoặc `1400px`) trên các trang quản trị tài nguyên chính (VMs, Networks, IAM, Storage, Tenants/Orgs). 
  * Chiều rộng vùng nội dung luôn chiếm `100%` phần không gian trống còn lại bên cạnh Sidebar (`calc(100vw - sidebarWidth)`).
* **Thống nhất căn lề dọc (Vertical Alignment Grid)**:
  * Tất cả các khối tiêu đề (Page Header), thanh công cụ lọc (Toolbar), và bảng dữ liệu (Data Grid/Table) đều sử dụng mức padding ngang đồng bộ là **24px** (`padding-inline: 24px` hay lớp Tailwind `px-6`).
* **Căn chỉnh thanh công cụ (Toolbar Alignment)**:
  * Thanh tìm kiếm chiếm khoảng `35% - 40%` chiều rộng khung nội dung.
  * Các nút tạo mới tài nguyên (ví dụ: `Create Organization`, `Create VM`) được di chuyển xuống cùng hàng với thanh tìm kiếm, căn lề phải để cân bằng bố cục khi màn hình mở rộng.
* **Các trang ngoại lệ**:
  * Chỉ các màn hình cấu hình thông tin tĩnh hoặc tổng quan ít dữ liệu (như Dashboard, Settings, User Profile) mới sử dụng container căn giữa và giới hạn chiều rộng tối đa (`max-w-[1200px] mx-auto`).
