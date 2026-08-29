import { WalletOnboarding } from "../onboarding/WalletOnboarding";

interface DashboardPageProps {
  personal: boolean;
}

// Overview deliberately renders only the wallet/onboarding projection that the
// verified owner branch can read. Revenue, margin, invoices and ledger history
// each need their own durable read workflow before they can appear here.
export default function DashboardPage({ personal }: DashboardPageProps) {
  return <WalletOnboarding personal={personal} />;
}
