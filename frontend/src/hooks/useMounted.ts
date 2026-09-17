"use client";

import { useEffect, useState } from "react";

// False on the server and on the first client render. Anything that depends on the stored theme
// renders its server form until then, so hydration matches.
export function useMounted(): boolean {
  const [mounted, setMounted] = useState(false);
  useEffect(() => setMounted(true), []);
  return mounted;
}
