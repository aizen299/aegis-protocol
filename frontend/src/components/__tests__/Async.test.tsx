import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import { Async } from "../Async";

afterEach(cleanup);

const settled = { isPending: false, isError: false, error: null };

describe("Async", () => {
  it("shows loading while the fetch is in flight", () => {
    render(
      <Async query={{ isPending: true, isError: false, error: null, data: undefined }} empty="none">
        {() => <p>never</p>}
      </Async>,
    );
    expect(screen.getByTestId("async-loading")).toBeInTheDocument();
  });

  // The distinction the whole module exists for: an unreachable API is not a protocol with nothing
  // in it.
  it("shows an unreachable API as a failure, not as an empty list", () => {
    render(
      <Async
        query={{ isPending: false, isError: true, error: { message: "connection refused" }, data: undefined }}
        empty="No feeds are registered."
      >
        {() => <p>never</p>}
      </Async>,
    );

    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent(/failure to read, not an absence/i);
    expect(alert).toHaveTextContent(/connection refused/);
    expect(screen.queryByText("No feeds are registered.")).not.toBeInTheDocument();
  });

  it("shows a genuinely empty list as empty, without an alert", () => {
    render(
      <Async query={{ ...settled, data: [] }} empty="No feeds are registered.">
        {() => <p>never</p>}
      </Async>,
    );

    expect(screen.getByTestId("async-empty")).toHaveTextContent("No feeds are registered.");
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("renders children when data arrives", () => {
    render(
      <Async query={{ ...settled, data: ["a"] }} empty="none">
        {(items) => <p>{items.length} item</p>}
      </Async>,
    );
    expect(screen.getByText("1 item")).toBeInTheDocument();
  });

  // A 200 whose body did not parse leaves data undefined. Rendering that as empty would repeat the
  // exact defect this project fixed one layer down.
  it("treats a settled query with no data as a failure", () => {
    render(
      <Async query={{ ...settled, data: undefined }} empty="none">
        {() => <p>never</p>}
      </Async>,
    );
    expect(screen.getByTestId("async-failed")).toBeInTheDocument();
  });
});
