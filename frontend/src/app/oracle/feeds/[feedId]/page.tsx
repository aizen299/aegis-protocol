import { RoundList } from "@/components/oracle/RoundList";

export default async function FeedPage({ params }: { params: Promise<{ feedId: string }> }) {
  const { feedId } = await params;
  return <RoundList feedId={feedId} />;
}
