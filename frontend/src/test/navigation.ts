import { vi } from "vitest";

// What next/navigation returns in tests; a test sets `search` or `pathname` to render as a chain or
// route.
export const navigation = {
  search: new URLSearchParams(),
  pathname: "/",
  push: vi.fn(),
};

export function renderOnChain(chain: string) {
  navigation.search = new URLSearchParams({ chain });
}
