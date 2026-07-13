import { fetchJSON } from "./fetcher";

// [COMMENT]: Khai báo base URL gọi trực tiếp tới dịch vụ Cost Manager API
const BILLING_API_BASE = "http://localhost:8084/api/v1/billing";

export type Wallet = {
  id: string;
  owner_id: string;
  owner_type: string;
  balance: number;
  currency: string;
  overdraft_limit: number;
  status: string; // 'ACTIVE', 'SUSPENDED'
  created_at: string;
  updated_at: string;
};

export type Transaction = {
  id: string;
  wallet_id: string;
  amount: number;
  tx_type: string; // 'DEPOSIT', 'USAGE_CHARGE', 'REFUND'
  service_type: string; // 'STORAGE', 'MAIL', 'VM'
  reference_id: string;
  description: string;
  created_at: string;
};

// [COMMENT]: GetWallet lấy hoặc khởi tạo ví của user/workspace chỉ định
export async function getWallet(
  ownerId: string,
  ownerType: string,
  signal?: AbortSignal
): Promise<Wallet> {
  const url = `${BILLING_API_BASE}/wallet?owner_id=${ownerId}&owner_type=${ownerType}`;
  // [COMMENT]: fetchJSON helper mặc định gọi local proxy, ở đây ta truyền absolute URL
  const res = await fetch(url, { signal });
  if (!res.ok) {
    throw new Error(await res.text() || "Failed to fetch wallet");
  }
  return res.json();
}

// [COMMENT]: Deposit thực hiện nạp tiền giả lập vào ví
export async function deposit(
  ownerId: string,
  ownerType: string,
  amount: number,
  description?: string,
  signal?: AbortSignal
): Promise<void> {
  const url = `${BILLING_API_BASE}/wallet/deposit`;
  const res = await fetch(url, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
      owner_id: ownerId,
      owner_type: ownerType,
      amount,
      description: description || "Nạp tiền ví điện tử",
    }),
    signal,
  });
  if (!res.ok) {
    throw new Error(await res.text() || "Failed to deposit");
  }
}

// [COMMENT]: GetTransactions lấy lịch sử giao dịch ví
export async function getTransactions(
  walletId: string,
  signal?: AbortSignal
): Promise<Transaction[]> {
  const url = `${BILLING_API_BASE}/wallet/${walletId}/transactions`;
  const res = await fetch(url, { signal });
  if (!res.ok) {
    throw new Error(await res.text() || "Failed to fetch transactions");
  }
  return res.json();
}
