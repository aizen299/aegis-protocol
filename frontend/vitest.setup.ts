import "@testing-library/jest-dom/vitest";

import { vi } from "vitest";

import { navigation } from "./src/test/navigation";

vi.mock("next/navigation", () => ({
  useSearchParams: () => navigation.search,
  usePathname: () => navigation.pathname,
  useRouter: () => ({ push: navigation.push, replace: navigation.push, prefetch: vi.fn() }),
}));
