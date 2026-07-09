# Aurora Cloud Console UI Design Guidelines

Tài liệu này đặc tả chi tiết phong cách thiết kế giao diện (Design System) của **Aurora Cloud Console**. 

Mục tiêu là định hình giao diện từ một **SaaS Dashboard** thông thường sang **Enterprise Cloud Control Plane** chuyên nghiệp dành cho người vận hành hạ tầng, lấy cảm hứng từ sự nhất quán của **Azure Portal (60%)**, cấu trúc Hairline Divider của **GitHub/HashiCorp (30%)**, và tối giản dữ liệu của **AWS Console (10%)**.

---

## 1. Triết lý thiết kế tổng quan (Design Philosophy)

* **Tập trung vào Dữ liệu (Data-Centric)**: Màu sắc và hiệu ứng hình ảnh đóng vai trò phụ trợ. Dữ liệu và trạng thái hệ thống phải là thứ nổi bật nhất.
* **Tối ưu hóa thời gian làm việc (High Endurance UI)**: Giao diện sử dụng tông màu dịu, giảm độ tương phản chói và độ lấp lánh (gradient, shadow lớn) để người vận hành có thể làm việc liên tục nhiều giờ mà không mỏi mắt.
* **Ưu tiên Hairline Divider hơn Card / Shadow (Divider First)**: 
  * Không lạm dụng bóng đổ (shadow) để phân tách các thành phần.
  * Hạn chế bọc card bo góc lồng nhau (card-in-card).
  * Sử dụng các đường viền hairline mỏng (`1px` hoặc `0.5px` trên màn hình Retina) kết hợp màu nền dịu để phân vùng giao diện rõ ràng, ngăn nắp.

---

## 2. Design Tokens cơ bản

### 2.1. Bo góc (Border Radius)
Tuyệt đối không sử dụng bo góc lớn (12px – 20px) cho các thành phần chính vì tạo cảm giác CRM/SaaS không chuyên nghiệp.

| Component | Radius Token | Lớp Tailwind tương ứng |
| :--- | :---: | :--- |
| **Card / Surface** | 4px – 6px | `rounded-lg` hoặc `rounded-[6px]` |
| **Button** | 4px – 6px | `rounded-md` |
| **Input / Search** | 6px | `rounded-md` |
| **Dropdown / Menu** | 6px | `rounded-md` |
| **Table Grid / Table Container** | 6px – 8px | `rounded-xl` |
| **Modal / Dialog** | 8px | `rounded-xl` |

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
  * **Page Title**: `20px` / Weight 700 (`text-xl font-bold` / Màu Text Primary)
  * **Section Title**: `16px` / Weight 600 (`text-base font-semibold`)
  * **Card Title**: `14px` / Weight 600 (`text-sm font-semibold`)
  * **Body / Sidebar / Table**: `13px` / Weight 400 hoặc 500 (`text-[13px] font-normal/font-medium`)
  * **Table Header / Captions**: `12px` / Weight 600 (`text-[12px] font-semibold`)
  * **Badge / Small Labels / Subtitles**: `11px` / Weight 600 (`text-[11px] font-bold`)
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

Màu trạng thái phải được định nghĩa nhất quán trên toàn bộ hệ thống. Đối với các bảng dữ liệu danh sách, **không lạm dụng Badge có viền bọc xung quanh trạng thái**. Sử dụng phong cách tinh gọn: **Chấm tròn trạng thái (Status Dot) + nhãn chữ (Label text)** có màu tương ứng:

| Trạng thái (Status) | Màu sắc (Color) | Mã màu Tailwind | Ý nghĩa |
| :--- | :---: | :---: | :--- |
| **Healthy / Active** | Green / Emerald | `text-emerald-500` / `bg-emerald-500` | Hoạt động bình thường, ổn định. |
| **Warning / Pending** | Amber | `text-amber-500` / `bg-amber-500` | Hiệu năng giảm, đang chờ hoặc cần lưu ý. |
| **Error / Suspended** | Red | `text-red-500` / `bg-red-500` | Gặp sự cố nghiêm trọng, dừng hoạt động. |
| **Info / Updating** | Blue | `text-blue-500` / `bg-blue-500` | Đang cập nhật, cấu hình thông tin. |
| **Disabled / Offline** | Gray | `text-slate-400` / `bg-slate-400` | Vô hiệu hóa, không hoạt động. |

---

## 4. Bố cục Chia cột Dọc (Master-Detail Divided Pane Layout)

Nhằm tối ưu hóa hiệu quả hiển thị khi cần xem nhanh (Quick Inspection) tài nguyên mà không làm gián đoạn luồng làm việc trên danh sách bảng, Aurora áp dụng cấu trúc **Divided Pane Layout**:

* **Tỷ lệ phân chia ngang hàng (67% / 33%)**:
  * Khi chọn xem chi tiết một tài nguyên (ví dụ: User, VM, Tenant), trang sẽ tự động chia làm 2 cột: Cột trái (Master) chiếm `2/3` (hoặc `67%`) và Cột phải (Detail) chiếm `1/3` (hoặc `33%`).
  * Cột bên trái sẽ bao trọn cả Header Section (Tiêu đề, mô tả và các action tổng của trang) cùng với bảng dữ liệu, đồng loạt dịch chuyển co giãn song song sang trái để nhường không gian bên phải cho Panel chi tiết.
  * Khi đóng chi tiết tài nguyên, Cột bên trái tự động giãn rộng ra chiếm trọn `100%` không gian hiển thị của trang.
* **Đường phân cách Dọc Cố định (Full-Height Hairline Divider)**:
  * Hai cột được ngăn cách bằng một đường hairline dọc duy nhất bám lề trái của cột thông tin (`border-l border-border/60`).
  * Sử dụng thuộc tính `items-stretch` của Flexbox kết hợp chiều cao tối thiểu lớn (`min-h-[calc(100vh-110px)]`) để đảm bảo đường phân cách dọc luôn kéo dài thẳng tắp suốt chiều dọc màn hình làm việc, không bị đứt đoạn theo độ dài của nội dung Detail Panel.

---

## 5. Cấu trúc Detail Panel (Hairline Section Style)

Tránh tuyệt đối thiết kế Detail Panel bên phải theo phong cách card bóng đổ xếp chồng chéo (card-in-card). Panel chi tiết phải được thiết kế phẳng hoàn toàn, tiệp vào nền trang:

* **Không bọc Card ngoài**: Panel ngoài cùng không có viền bao xung quanh (`border-none`), không bo góc ngoài (`rounded-none`), không đổ bóng (`shadow-none`) và sử dụng nền phẳng (`bg-transparent`).
* **Section chia bằng Hairline ngang**:
  * Các khối thông tin chi tiết (ví dụ: `Basic Information`, `Security Summary`, `Roles`, `Devices`...) được hiển thị dạng phẳng trần.
  * Phân vùng các section hoàn toàn bằng các đường hairline ngang mảnh (`border-b border-border/60 pb-5 mb-1 last:border-0 last:pb-0`).
  * Nhãn tiêu đề section viết in hoa, cỡ chữ nhỏ và in đậm (`text-[11px] font-bold text-foreground uppercase tracking-wider block mb-2`).

---

## 6. Sidebar & Navigation Menu

Thanh điều hướng bên (Sidebar) được thiết kế theo phong cách tối giản của Azure Portal:

* **Chiều cao item**: Rút gọn xuống `h-8` thay vì `h-10` để hiển thị nhiều menu dịch vụ hơn.
* **Cỡ chữ**: Dùng `text-xs font-medium` để hiển thị nhãn ngắn gọn.
* **Active State**: Làm nổi bật rõ nét bằng đường border trái mỏng (`3px`) màu xanh thương hiệu (`#3B82F6`) và màu nền mờ phủ nhẹ (`bg-sidebar-console-active-bg`).
* **Hover State**: Thay đổi màu nền rất nhẹ (`hover:bg-sidebar-console-hover`).
* **Sidebar Token**:
  * **Light Sidebar Background**: `#111827` (Gray 900)
  * **Dark Sidebar Background**: `#181A1E`

---

## 7. Thiết kế ưu tiên Bảng dữ liệu (Table-First Design)

Đối với các màn hình quản trị tài nguyên hạ tầng (Virtual Machines, Kubernetes, Storage, Tenants/Orgs, IAM, v.v.), **không lạm dụng dạng lưới Card View**.

* **Mặc định**: Luôn hiển thị dữ liệu dưới dạng **Bảng (Table) hoặc Data Grid**.
* **Card View**: Chỉ cung cấp dưới dạng tùy chọn phụ (Toggle View Mode) hoặc dành cho trang Overview tổng quan.
* **Cấu trúc bảng**:
  * Đường phân chia hàng mỏng nhẹ (`divide-border/40`).
  * Padding ô dữ liệu hẹp (`py-2 px-3`) để tăng mật độ thông tin.
  * Tên tài nguyên (ví dụ: VM Name, Tenant Name) luôn được in đậm rõ nét.

---

## 8. Tối giản hóa Bộ lọc (Flat Integrated Filters Bar)

* **Nhúng phẳng vào Container Table**:
  * Khối bộ lọc (Filters Bar) không có đường viền bọc riêng và không có background độc lập nổi lên trên.
  * Nó được thiết kế phẳng hoàn toàn, nhúng trực tiếp làm hàng trên cùng (Header partition) của Table Container, chia cắt với danh sách bảng bên dưới bằng đường hairline ngang (`divide-y divide-border`).
* **Dropdown Lọc Tích hợp Nhãn (Inline Dropdown Labels)**:
  * Loại bỏ các nhãn văn bản (`span`) hiển thị rời rạc ở phía bên trái của các hộp select dropdown để tối ưu diện tích.
  * Nhúng trực tiếp nhãn vai trò vào các option hiển thị của select dropdown. 
  * *Ví dụ*: `Status: Active`, `Role: All`, `MFA: Enabled`, `Risk: Low`.
* **Nút Reset / Clear**:
  * Đi kèm icon xoay đặt lại (`RotateCcw`) đứng trước nhãn.
  * Màu chữ nổi bật nhẹ nhàng bằng tông màu xanh nước biển thương hiệu (`text-blue-600 dark:text-blue-400`).

---

## 9. Khả năng tiếp cận & Trạng thái tương tác (Accessibility & States)

### 9.1. Tiêu chuẩn tiếp cận WCAG AA
* **Độ tương phản**: Độ tương phản chữ chính trên nền tối thiểu đạt tỷ lệ **4.5:1** để có thể đọc rõ ràng trong môi trường ánh sáng yếu.
* **Không dùng màu sắc đơn độc để báo hiệu trạng thái**: 
  * Luôn đi kèm với biểu tượng chỉ dẫn hoặc văn bản cụ thể.
  * *Ví dụ*: Hiển thị là `✔ Healthy` hoặc `⚠ Warning` hoặc `✖ Failed`.

### 9.2. Đầy đủ các Interactive States
Mọi thành phần tương tác (Button, Input, Menu Item) đều phải định nghĩa rõ các lớp trạng thái:
* **Default**: Trạng thái hiển thị ban đầu.
* **Hover**: Phản hồi khi di chuột qua (tăng nhẹ độ sáng hoặc đổi nền mờ).
* **Pressed / Active**: Phản hồi khi click chuột xuống.
* **Focused**: Đường ring bao quanh khi duyệt bằng phím Tab (`focus-visible:ring`).
* **Disabled**: Mờ đi (opacity 50%) và ngắt toàn bộ sự kiện chuột.

---

## 10. Quy tắc bố cục co giãn & Thích ứng (Responsive & Fluid Layout Rule)

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
