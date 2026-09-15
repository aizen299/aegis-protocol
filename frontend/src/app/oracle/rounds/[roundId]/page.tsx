import { RoundDetail } from "@/components/oracle/RoundDetail";

export default async function RoundPage({ params }: { params: Promise<{ roundId: string }> }) {
  const { roundId } = await params;
  return <RoundDetail roundId={roundId} />;
}
