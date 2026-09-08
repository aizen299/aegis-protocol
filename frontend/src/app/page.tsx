import { VaultDashboard } from "@/components/VaultDashboard";

export default function Home() {
  return (
    <main className="mx-auto flex min-h-screen max-w-3xl flex-col gap-6 px-6 py-12">
      <header className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold text-zinc-100">Aegis Protocol</h1>
          <p className="text-sm text-zinc-500">Vault Engine &middot; v0.1</p>
        </div>
      </header>
      <VaultDashboard />
    </main>
  );
}
