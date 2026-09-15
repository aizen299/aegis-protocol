"use client";

import Link from "next/link";

import { Async } from "@/components/Async";
import { Panel } from "@/components/Panel";
import { Cell, Row, Table } from "@/components/Table";
import { shortId } from "@/lib/format";
import { useOracleFeeds } from "@/lib/queries";

export function FeedList() {
  const feeds = useOracleFeeds();

  return (
    <Panel title="Oracle feeds">
      <Async query={{ ...feeds, data: feeds.data?.items }} empty="No feeds are registered.">
        {(items) => (
          <Table headers={["Feed", "Id", "Decimals", "Status"]}>
            {items.map((feed) => (
              <Row key={feed.feedId}>
                <Cell>
                  <Link
                    className="text-sky-300 hover:underline"
                    href={`/oracle/feeds/${feed.feedId}`}
                  >
                    {feed.name}
                  </Link>
                </Cell>
                <Cell mono>{shortId(feed.feedId)}</Cell>
                <Cell mono>{feed.decimals}</Cell>
                <Cell>{feed.active ? "active" : "inactive"}</Cell>
              </Row>
            ))}
          </Table>
        )}
      </Async>
    </Panel>
  );
}
