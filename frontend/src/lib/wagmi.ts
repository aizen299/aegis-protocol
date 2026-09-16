import { connectorsForWallets } from "@rainbow-me/rainbowkit";
import { injectedWallet, metaMaskWallet, rainbowWallet, walletConnectWallet } from "@rainbow-me/rainbowkit/wallets";
import { arbitrum, arbitrumSepolia, foundry } from "wagmi/chains";
import { mock } from "wagmi/connectors";
import { createConfig, http } from "wagmi";

import { env } from "./env";
import { testWalletAccount } from "./testWallet";

const chains = [foundry, arbitrumSepolia, arbitrum] as const;

const walletConnectors = connectorsForWallets(
  [{ groupName: "Wallets", wallets: [injectedWallet, metaMaskWallet, rainbowWallet, walletConnectWallet] }],
  { appName: "Aegis Protocol", projectId: env.walletConnectProjectId || "aegis-local" },
);

const testAccount = testWalletAccount({ flag: env.testWallet, chainId: env.chainId });

export const testWalletEnabled = testAccount !== undefined;

export const wagmiConfig = createConfig({
  chains,
  connectors: testAccount
    ? [...walletConnectors, mock({ accounts: [testAccount], features: { reconnect: true } })]
    : walletConnectors,
  transports: {
    [foundry.id]: http(env.rpcUrl),
    [arbitrumSepolia.id]: http(),
    [arbitrum.id]: http(),
  },
  ssr: true,
});
