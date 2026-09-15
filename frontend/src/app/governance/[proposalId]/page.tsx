import { ProposalDetail } from "@/components/governance/ProposalDetail";

export default async function ProposalPage({
  params,
}: {
  params: Promise<{ proposalId: string }>;
}) {
  const { proposalId } = await params;
  return <ProposalDetail proposalId={proposalId} />;
}
