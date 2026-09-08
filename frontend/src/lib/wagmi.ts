import { getDefaultConfig } from "@rainbow-me/rainbowkit";
import { arbitrum, arbitrumSepolia, foundry } from "wagmi/chains";
import { http } from "wagmi";

import { env } from "./env";

const chains = [foundry, arbitrumSepolia, arbitrum] as const;

export const wagmiConfig = getDefaultConfig({
  appName: "Aegis Protocol",
  projectId: env.walletConnectProjectId || "aegis-local",
  chains,
  transports: {
    [foundry.id]: http(env.rpcUrl),
    [arbitrumSepolia.id]: http(),
    [arbitrum.id]: http(),
  },
  ssr: true,
});
