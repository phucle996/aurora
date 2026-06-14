import { useZoneStore } from '@/hooks/useZoneStore'

export type ScopeRequirement = 'global' | 'zone' | 'any'

export interface FeatureScopeConfig {
  write: ScopeRequirement
  read: ScopeRequirement
}

export const FEATURE_SCOPES: Record<string, FeatureScopeConfig> = {
  zones: {
    write: 'global', // Chỉ tạo/xóa Zone được khi ở chế độ Global
    read: 'any',
  },
  endpoints: {
    write: 'zone',   // Chỉ tạo Endpoint được khi đã chọn 1 Zone cụ thể
    read: 'any',
  },
  gateways: {
    write: 'zone',   // Chỉ deploy Gateway khi đứng ở 1 Zone cụ thể
    read: 'any',
  },
  tenants: {
    write: 'global', // Chỉ tạo Tenant mới ở cấp độ Global
    read: 'any',
  },
}

export function useFeatureScope(featureName: keyof typeof FEATURE_SCOPES) {
  // Đọc activeZone từ Zustand store (null = global, string = zone cụ thể)
  const activeZone = useZoneStore((state) => state.activeZone)
  
  // Xác định scope hiện tại của UI
  const currentScope = activeZone === null ? 'global' : 'zone'
  const config = FEATURE_SCOPES[featureName]

  // Hàm so khớp phạm vi yêu cầu
  const checkAccess = (requirement: ScopeRequirement): boolean => {
    if (requirement === 'any') return true
    return requirement === currentScope
  }

  return {
    /** Cho phép thực hiện thao tác sửa đổi (Create/Update/Delete) hay không */
    canWrite: checkAccess(config.write),
    /** Cho phép xem dữ liệu của tính năng này hay không */
    canRead: checkAccess(config.read),
    /** Phạm vi hoạt động hiện tại ('global' | 'zone') */
    currentScope,
    /** Mã zone hoạt động hiện tại (null nếu ở global) */
    activeZone,
  }
}
