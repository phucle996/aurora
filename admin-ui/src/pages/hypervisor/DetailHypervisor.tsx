/**
 * DetailHypervisor.tsx — Trang chi tiết của một Hypervisor Node.
 *
 * (Cleaned cũ) Tạm thời biểu diễn chi tiết trống/cơ bản của node để giữ build pass,
 * chuẩn bị cho việc tích hợp thêm các API chi tiết sau này.
 */

import { Link, useParams } from '@tanstack/react-router'
import { ArrowLeft, Server } from 'lucide-react'
import { PageContent } from '@/components/layout/layout'
import { Button } from '@/components/ui/button'

export default function DetailHypervisorPage() {
  // [COMMENT]: Lấy tham số agentId từ React Router URL Path
  const { agentId } = useParams({ from: '/hypervisor/$agentId' })

  return (
    <PageContent>
      <div className="space-y-4">
        {/* Nút quay lại danh sách */}
        <div className="flex items-center gap-2">
          <Button variant="ghost" size="sm" asChild>
            <Link to="/hypervisor" className="flex items-center gap-2">
              <ArrowLeft className="size-4" />
              Back to Hypervisors
            </Link>
          </Button>
        </div>

        {/* Panel thông tin chung */}
        <div className="rounded-2xl border border-border/60 bg-card p-6 shadow-sm">
          <div className="flex items-center gap-3">
            <div className="p-3 bg-primary/10 rounded-xl">
              <Server className="size-6 text-primary" />
            </div>
            <div>
              <h1 className="text-2xl font-bold text-foreground">Hypervisor Node Detail</h1>
              <p className="text-sm text-muted-foreground">Viewing node ID: {agentId}</p>
            </div>
          </div>

          <div className="mt-6 border-t border-border/60 pt-6">
            <p className="text-sm text-muted-foreground">
              Node detail monitoring dashboard is currently empty.
            </p>
          </div>
        </div>
      </div>
    </PageContent>
  )
}
