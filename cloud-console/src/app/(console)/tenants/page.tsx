"use client";

import React, { useState, useCallback, useMemo } from "react";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import { AnimatedTabs, TabItem } from "@/components/ui/animated-tabs";
import {
  Building2,
  Mail,
  Plus,
  Search,
  LayoutGrid,
  List,
  ArrowUpRight,
  MoreVertical,
  Check,
  X,
  Users2,
  FolderOpen
} from "lucide-react";

interface TenantItem {
  id: string;
  name: string;
  code: string;
  role: string;
  description: string;
  workspacesCount: number;
  membersCount: number;
}

interface InvitationItem {
  id: string;
  orgName: string;
  invitedBy: string;
  role: string;
  timeAgo: string;
  expiresIn: string;
}

export default function TenantsPage() {
  const router = useRouter();
  // [COMMENT]: Quản lý trạng thái tab hoạt động ở trang tenants (mặc định là organizations)
  const [activeTab, setActiveTab] = useState<string>("organizations");

  // [COMMENT]: Quản lý trạng thái tìm kiếm tổ chức
  const [searchQuery, setSearchQuery] = useState("");

  // [COMMENT]: Chế độ hiển thị mặc định dạng List (danh sách) thay vì Grid theo chuẩn thiết kế Table-first của Enterprise Control Plane
  const [viewMode, setViewMode] = useState<"grid" | "list">("list");

  // [COMMENT]: Khởi tạo danh sách tổ chức mockup ban đầu (đã loại bỏ thông tin Zones)
  const [organizations, setOrganizations] = useState<TenantItem[]>([
    {
      id: "org-1",
      name: "Aurora Labs",
      code: "aurora-labs",
      role: "owner",
      description: "Aurora Labs is our internal organization for building the future of cloud infrastructure.",
      workspacesCount: 12,
      membersCount: 48,
    },
    {
      id: "org-2",
      name: "Acme Corporation",
      code: "acme-corp",
      role: "admin",
      description: "Acme Corporation's cloud resources and infrastructure management.",
      workspacesCount: 6,
      membersCount: 23,
    },
    {
      id: "org-3",
      name: "Stark Industries",
      code: "stark-ind",
      role: "member",
      description: "Advanced defense systems and clean energy cloud projects.",
      workspacesCount: 4,
      membersCount: 89,
    },
  ]);

  // [COMMENT]: Khởi tạo danh sách thư mời tham gia tổ chức mockup ban đầu
  const [invitations, setInvitations] = useState<InvitationItem[]>([
    {
      id: "invite-1",
      orgName: "Oscorp Industries",
      invitedBy: "norman.osborn@oscorp.com",
      role: "developer",
      timeAgo: "2 hours ago",
      expiresIn: "6 days left",
    },
    {
      id: "invite-2",
      orgName: "Wayne Enterprises",
      invitedBy: "lucius.fox@wayne.com",
      role: "admin",
      timeAgo: "1 day ago",
      expiresIn: "3 days left",
    },
  ]);

  // [COMMENT]: Cấu hình danh sách tab động, truyền nhãn và số lượng badge cho thư mời nhận được
  const tabs = useMemo<TabItem[]>(() => [
    { id: "organizations", label: "Organizations" },
    { id: "invitations", label: "Invitations", count: invitations.length }
  ], [invitations.length]);

  // [COMMENT]: Chấp nhận thư mời tham gia tổ chức
  const handleAcceptInvite = useCallback((id: string) => {
    const invite = invitations.find((i) => i.id === id);
    if (!invite) return;

    // [COMMENT]: Thêm tổ chức mới vào danh sách người dùng quản lý
    const newOrg: TenantItem = {
      id: `org-${Date.now()}`,
      name: invite.orgName,
      code: invite.orgName.toLowerCase().replace(/[^a-z0-9-_]/g, "-"),
      role: "developer",
      description: `Organization joined via invitation from ${invite.invitedBy}.`,
      workspacesCount: 1,
      membersCount: 12,
    };

    setOrganizations((prev) => [...prev, newOrg]);
    setInvitations((prev) => prev.filter((i) => i.id !== id));
    toast.success(`Successfully joined Wayne Enterprises!`);
  }, [invitations]);

  // [COMMENT]: Từ chối thư mời tham gia tổ chức
  const handleDeclineInvite = useCallback((id: string) => {
    const invite = invitations.find((i) => i.id === id);
    if (!invite) return;

    setInvitations((prev) => prev.filter((i) => i.id !== id));
    toast.info(`Declined Wayne Enterprises invitation.`);
  }, [invitations]);

  // [COMMENT]: Lọc danh sách tổ chức theo truy vấn tìm kiếm
  const filteredOrgs = useMemo(() => {
    return organizations.filter(
      (org) =>
        org.name.toLowerCase().includes(searchQuery.toLowerCase()) ||
        org.description.toLowerCase().includes(searchQuery.toLowerCase()) ||
        org.code.toLowerCase().includes(searchQuery.toLowerCase())
    );
  }, [organizations, searchQuery]);

  return (
    <div className="space-y-5">
      {/* Menu điều hướng Tab cục bộ sử dụng component dùng chung có hiệu ứng chuyển động */}
      <AnimatedTabs
        tabs={tabs}
        activeTab={activeTab}
        onChange={setActiveTab}
      />

      {activeTab === "organizations" && (
        <>
          {/* [COMMENT]: Header của tab Organizations chỉ chứa tiêu đề và mô tả ngắn gọn */}
          <div className="flex flex-col gap-0.5 select-none">
            <h1 className="text-2xl font-semibold tracking-tight text-slate-900 dark:text-slate-100">Organizations</h1>
            <p className="text-[13px] font-normal text-slate-500 dark:text-slate-400">Manage the organizations you own or are a member of.</p>
          </div>

          {/* [COMMENT]: Toolbar chứa tìm kiếm, bộ chuyển đổi view và nút hành động chính trên một hàng ngang co giãn */}
          <div className="flex flex-col sm:flex-row items-center justify-between gap-3 bg-white dark:bg-slate-950 p-2 border border-slate-200 dark:border-slate-800 rounded-[4px] select-none">
            
            {/* [COMMENT]: Khối bên trái gồm thanh tìm kiếm (w-[320px]) và nút chuyển đổi Grid/List view */}
            <div className="flex items-center gap-3 w-full sm:w-auto">
              <div className="relative w-full sm:w-[320px]">
                <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-slate-400" />
                <input
                  type="text"
                  placeholder="Search organizations..."
                  value={searchQuery}
                  onChange={(e) => setSearchQuery(e.target.value)}
                  className="w-full bg-slate-50 hover:bg-slate-100/50 focus:bg-white dark:bg-slate-900 dark:hover:bg-slate-850 dark:focus:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-[4px] pl-9 pr-4 h-8 text-xs text-slate-700 dark:text-slate-300 outline-none transition-all"
                />
              </div>

              {/* Bộ chuyển đổi Grid / List view */}
              <div className="flex items-center gap-0.5 bg-slate-100 dark:bg-slate-900 rounded-[4px] p-0.5 shrink-0">
                <button
                  onClick={() => setViewMode("grid")}
                  className={`p-1 rounded-[3px] transition-all cursor-pointer ${viewMode === "grid"
                      ? "bg-white text-blue-500 dark:bg-slate-800"
                      : "text-slate-400 hover:text-slate-600 dark:hover:text-slate-300"
                    }`}
                >
                  <LayoutGrid className="h-3.5 w-3.5" />
                </button>
                <button
                  onClick={() => setViewMode("list")}
                  className={`p-1 rounded-[3px] transition-all cursor-pointer ${viewMode === "list"
                      ? "bg-white text-blue-500 dark:bg-slate-800"
                      : "text-slate-400 hover:text-slate-600 dark:hover:text-slate-300"
                    }`}
                >
                  <List className="h-3.5 w-3.5" />
                </button>
              </div>
            </div>

            {/* [COMMENT]: Khối bên phải gồm nút tạo tổ chức mới, căn phải trên màn hình lớn */}
            <div className="w-full sm:w-auto flex justify-end gap-2 shrink-0">
              <button
                onClick={() => router.push("/tenants/new")}
                className="w-full sm:w-auto h-8 px-3 bg-blue-600 hover:bg-blue-700 text-white text-xs font-medium rounded-[4px] cursor-pointer flex items-center justify-center gap-1.5 transition-all"
              >
                <Plus className="h-3.5 w-3.5" />
                Create Organization
              </button>
            </div>
          </div>

          {/* [COMMENT]: Giao diện trống (Empty state) bo góc 4px, không shadow, giảm padding dọc */}
          {filteredOrgs.length === 0 && (
            <div className="py-12 bg-white dark:bg-slate-950 border border-slate-200 dark:border-slate-800 rounded-[4px] text-center space-y-3.5 max-w-md mx-auto">
              <div className="mx-auto flex h-10 w-10 items-center justify-center rounded-[4px] bg-blue-50 dark:bg-blue-950/30 text-blue-500">
                <Building2 className="h-5 w-5" />
              </div>
              <div className="space-y-0.5">
                <h3 className="text-xs font-bold text-slate-800 dark:text-slate-200">No organizations found</h3>
                <p className="text-[11px] text-slate-450 dark:text-slate-500">Try adjusting your search criteria or create a new organization.</p>
              </div>
            </div>
          )}

          {/* [COMMENT]: Grid Layout view với khoảng cách gap-4, card bo góc 4px, p-4 hẹp và không shadow */}
          {viewMode === "grid" && filteredOrgs.length > 0 && (
            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
              {filteredOrgs.map((org) => (
                <div
                  key={org.id}
                  className="bg-white dark:bg-slate-950 border border-slate-200 dark:border-slate-800 rounded-[4px] p-4 flex flex-col justify-between hover:border-slate-350 dark:hover:border-slate-700 hover:bg-slate-50/20 dark:hover:bg-slate-900/5 transition-all duration-150 group relative"
                >
                  <div className="space-y-2.5">
                    {/* Header Card */}
                    <div className="flex justify-between items-start">
                      <div className="flex items-center gap-2.5">
                        <div className="h-8 w-8 bg-blue-50 dark:bg-blue-950/30 text-blue-600 dark:text-blue-400 rounded-[4px] flex items-center justify-center font-bold text-xs select-none uppercase">
                          {org.name.slice(0, 2)}
                        </div>
                        <div>
                          {/* [COMMENT]: Tên thực thể chính nổi bật hơn (15px font-semibold) */}
                          <h3 className="text-[15px] font-semibold text-slate-900 dark:text-slate-100 flex items-center gap-1">
                            {org.name}
                          </h3>
                          <span className="font-mono text-[12px] text-slate-500 dark:text-slate-400 block">{org.code}</span>
                        </div>
                      </div>

                      {/* Badge phân quyền (bo góc 4px) và nút thao tác */}
                      <div className="flex items-center gap-1.5">
                        <span className="px-1.5 py-0.5 rounded-[4px] text-[11px] font-semibold capitalize bg-slate-100 dark:bg-slate-900 text-slate-550 dark:text-slate-400">
                          {org.role}
                        </span>
                        <button className="p-1 rounded-[4px] text-slate-400 hover:bg-slate-50 dark:hover:bg-slate-900 hover:text-slate-700 dark:hover:text-slate-200 transition-all cursor-pointer">
                          <MoreVertical className="h-3.5 w-3.5" />
                        </button>
                      </div>
                    </div>

                    {/* Mô tả ngắn gọn */}
                    <p className="text-[12px] text-slate-500 dark:text-slate-400 leading-normal line-clamp-2">
                      {org.description}
                    </p>
                  </div>

                  {/* [COMMENT]: Metadata tổ chức với text-[12px] dễ đọc */}
                  <div className="border-t border-slate-100 dark:border-slate-900/60 mt-3.5 pt-2.5 flex items-center justify-between text-[12px] text-slate-500 dark:text-slate-400 select-none">
                    <div className="flex items-center gap-3">
                      <div className="flex items-center gap-1">
                        <FolderOpen className="h-3 w-3 text-slate-400" />
                        <span><strong>{org.workspacesCount}</strong> Workspaces</span>
                      </div>
                      <div className="flex items-center gap-1">
                        <Users2 className="h-3 w-3 text-slate-400" />
                        <span><strong>{org.membersCount}</strong> Members</span>
                      </div>
                    </div>

                    {/* Nút hành động trực tiếp */}
                    <button className="text-blue-500 hover:text-blue-600 text-[12px] font-semibold flex items-center gap-0.5 opacity-0 group-hover:opacity-100 transition-opacity cursor-pointer">
                      Enter
                      <ArrowUpRight className="h-3.5 w-3.5" />
                    </button>
                  </div>
                </div>
              ))}
            </div>
          )}

          {/* [COMMENT]: List Layout (Table) với wrapper bo góc 4px, không shadow, giảm padding ô dữ liệu py-2 px-3 */}
          {viewMode === "list" && filteredOrgs.length > 0 && (
            <div className="bg-white dark:bg-slate-950 border border-slate-200 dark:border-slate-800 rounded-[4px] overflow-hidden">
              <table className="w-full text-left border-collapse">
                <thead>
                  {/* [COMMENT]: Tiêu đề cột nâng lên text-[12px] font-semibold */}
                  <tr className="border-b border-slate-150 dark:border-slate-900/80 text-[12px] text-slate-550 dark:text-slate-400 font-semibold uppercase select-none">
                    <th className="py-2 px-3 w-1/4">Name</th>
                    <th className="py-2 px-2 w-1/5">Unique Code</th>
                    <th className="py-2 px-2">Role</th>
                    <th className="py-2 px-2 text-center">Workspaces</th>
                    <th className="py-2 px-2 text-center">Members</th>
                    <th className="py-2 px-3 text-right">Actions</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-100/50 dark:divide-slate-900/50 text-[13px]">
                  {filteredOrgs.map((org) => (
                    <tr
                      key={org.id}
                      className="hover:bg-slate-50/50 dark:hover:bg-slate-900/10 group transition-all"
                    >
                      <td className="py-2 px-3">
                        <div className="flex items-center gap-2">
                          <div className="h-6 w-6 bg-blue-50 dark:bg-blue-950/20 text-blue-600 dark:text-blue-400 rounded-[4px] flex items-center justify-center font-bold text-[11px] uppercase shrink-0">
                            {org.name.slice(0, 2)}
                          </div>
                          <div className="truncate">
                            {/* [COMMENT]: Tên tổ chức dùng cỡ chữ 14px font-semibold */}
                            <span className="font-semibold text-slate-900 dark:text-slate-100 text-sm block truncate">{org.name}</span>
                            <span className="text-[12px] text-slate-500 dark:text-slate-400 max-w-[280px] truncate block leading-normal">{org.description}</span>
                          </div>
                        </div>
                      </td>
                      <td className="py-2 px-2 font-mono text-[12px] text-slate-500 dark:text-slate-400">{org.code}</td>
                      <td className="py-2 px-2 capitalize">
                        <span className="px-1.5 py-0.5 rounded-[4px] text-[11px] font-semibold bg-slate-100 dark:bg-slate-900 text-slate-550 dark:text-slate-400">
                          {org.role}
                        </span>
                      </td>
                      <td className="py-2 px-2 text-center text-slate-700 dark:text-slate-300">{org.workspacesCount}</td>
                      <td className="py-2 px-2 text-center text-slate-700 dark:text-slate-300">{org.membersCount}</td>
                      <td className="py-2 px-3 text-right">
                        <div className="flex items-center justify-end gap-2.5">
                          <button className="text-blue-500 hover:text-blue-600 text-[12px] font-semibold opacity-0 group-hover:opacity-100 transition-opacity flex items-center gap-0.5 cursor-pointer">
                            Enter
                            <ArrowUpRight className="h-3.5 w-3.5" />
                          </button>
                          <button className="p-1 rounded-[4px] text-slate-400 hover:bg-slate-100 dark:hover:bg-slate-900 transition-all cursor-pointer">
                            <MoreVertical className="h-3.5 w-3.5" />
                          </button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}

      {activeTab === "invitations" && (
        <div className="space-y-4">
          <div className="space-y-0.5">
            <h1 className="text-2xl font-semibold tracking-tight text-slate-900 dark:text-slate-100">Invitations</h1>
            <p className="text-[13px] font-normal text-slate-500 dark:text-slate-400">Accept or decline invitations to join existing organizations.</p>
          </div>

          {invitations.length === 0 ? (
            <div className="py-12 bg-white dark:bg-slate-950 border border-slate-200 dark:border-slate-800 rounded-[4px] text-center space-y-3.5 max-w-md mx-auto">
              <div className="mx-auto flex h-10 w-10 items-center justify-center rounded-[4px] bg-slate-100 dark:bg-slate-900 text-slate-400">
                <Mail className="h-5 w-5" />
              </div>
              <div className="space-y-0.5">
                <h3 className="text-xs font-bold text-slate-800 dark:text-slate-200">No pending invitations</h3>
                <p className="text-[11px] text-slate-450 dark:text-slate-500">You are all caught up. New invitations will show up here.</p>
              </div>
            </div>
          ) : (
            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
              {invitations.map((invite) => (
                <div
                  key={invite.id}
                  className="bg-white dark:bg-slate-950 border border-slate-200 dark:border-slate-800 rounded-[4px] p-4 space-y-3 hover:border-slate-350 dark:hover:border-slate-700 transition-all duration-150"
                >
                  <div className="flex justify-between items-start">
                    <div className="flex items-center gap-2.5">
                      <div className="h-8 w-8 bg-indigo-50 dark:bg-indigo-950/20 text-indigo-600 dark:text-indigo-400 rounded-[4px] flex items-center justify-center font-bold text-[11px] uppercase">
                        {invite.orgName.slice(0, 2)}
                      </div>
                      <div>
                        {/* [COMMENT]: Tên tổ chức lời mời dùng cỡ chữ 14px */}
                        <h3 className="text-[14px] font-semibold text-slate-900 dark:text-slate-100">{invite.orgName}</h3>
                        <span className="text-[12px] text-slate-500 dark:text-slate-400">Invited by: {invite.invitedBy}</span>
                      </div>
                    </div>
                    <span className="text-[12px] text-slate-500 dark:text-slate-400 font-semibold">{invite.timeAgo}</span>
                  </div>

                  <div className="flex justify-between items-center text-[12px] text-slate-500 dark:text-slate-400 border-y border-slate-100 dark:border-slate-900/60 py-1.5">
                    <span>Role offered: <strong className="text-slate-700 dark:text-slate-300 capitalize font-semibold">{invite.role}</strong></span>
                    <span className="text-amber-600 dark:text-amber-500 font-semibold">{invite.expiresIn}</span>
                  </div>

                  <div className="flex justify-end gap-2.5 pt-0.5">
                    <button
                      onClick={() => handleDeclineInvite(invite.id)}
                      className="h-8 px-3 border border-slate-200 dark:border-slate-800 hover:bg-slate-100 dark:hover:bg-slate-900 text-xs font-medium rounded-[4px] text-slate-750 dark:text-slate-350 cursor-pointer flex items-center gap-1 transition-all"
                    >
                      <X className="h-3.5 w-3.5" />
                      Decline
                    </button>
                    <button
                      onClick={() => handleAcceptInvite(invite.id)}
                      className="h-8 px-3 bg-blue-600 hover:bg-blue-700 text-white text-xs font-medium rounded-[4px] cursor-pointer flex items-center gap-1 transition-all"
                    >
                      <Check className="h-3.5 w-3.5" />
                      Accept & Join
                    </button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
