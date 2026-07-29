import { SocialLinksScreen } from "@/features/settings/social-links-screen";

type SocialLinksPageProps = {
  searchParams: Promise<{ social_link?: string | string[] }>;
};

export default async function SocialLinksPage({ searchParams }: SocialLinksPageProps) {
  const rawOutcome = (await searchParams).social_link;
  const outcome = rawOutcome === "linked" || rawOutcome === "failed" ? rawOutcome : undefined;
  return <SocialLinksScreen callbackOutcome={outcome} />;
}
