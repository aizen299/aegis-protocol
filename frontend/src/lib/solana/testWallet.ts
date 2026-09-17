import {
  BaseSignerWalletAdapter,
  WalletNotConnectedError,
  WalletReadyState,
  type TransactionOrVersionedTransaction,
  type WalletName,
} from "@solana/wallet-adapter-base";
import { Keypair, Transaction, type PublicKey } from "@solana/web3.js";

export const SOLANA_TEST_WALLET = "Test wallet (localnet)" as WalletName<"Test wallet (localnet)">;
const STORAGE_KEY = "aegis:solana-localnet-test-key";

const icon =
  "data:image/svg+xml;base64," +
  btoa('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><rect width="24" height="24" rx="6" fill="#F59E0B"/><path d="M7 12h10M12 7v10" stroke="#0F172A" stroke-width="2.5" stroke-linecap="round"/></svg>');

// A keypair generated in this browser and kept in its local storage, for exercising write paths against
// solana-localnet without an extension. Offered only there: see solanaTestWalletEnabled.
export class LocalnetTestWalletAdapter extends BaseSignerWalletAdapter<"Test wallet (localnet)"> {
  name = SOLANA_TEST_WALLET;
  url = "https://docs.solana.com/cli/examples/test-validator";
  icon = icon;
  // Installed, not Loadable: it needs no extension, and autoConnect reconnects only installed wallets.
  readyState = WalletReadyState.Installed;
  supportedTransactionVersions = null;
  connecting = false;
  private keypair: Keypair | null = null;

  get publicKey(): PublicKey | null {
    return this.keypair?.publicKey ?? null;
  }

  async connect(): Promise<void> {
    this.keypair = loadOrCreateKeypair();
    this.emit("connect", this.keypair.publicKey);
  }

  async disconnect(): Promise<void> {
    this.keypair = null;
    this.emit("disconnect");
  }

  async signTransaction<T extends TransactionOrVersionedTransaction<null>>(transaction: T): Promise<T> {
    if (!this.keypair) throw new WalletNotConnectedError();
    if (!(transaction instanceof Transaction)) throw new Error("the test wallet signs legacy transactions only");
    transaction.partialSign(this.keypair);
    return transaction;
  }
}

function loadOrCreateKeypair(): Keypair {
  try {
    const stored = window.localStorage.getItem(STORAGE_KEY);
    if (stored) return Keypair.fromSecretKey(Uint8Array.from(JSON.parse(stored) as number[]));
  } catch {
    // An unreadable key is replaced; it held nothing that exists off this localnet.
  }
  const keypair = Keypair.generate();
  try {
    window.localStorage.setItem(STORAGE_KEY, JSON.stringify([...keypair.secretKey]));
  } catch {
    // Without storage the wallet still works for this page's lifetime.
  }
  return keypair;
}
