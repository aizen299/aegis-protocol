"use client";

import { useEffect, useState } from "react";
import { bytesToHex } from "viem";
import { useAccount, useChainId, usePublicClient } from "wagmi";

import { Panel } from "@/components/Panel";
import { TxButton, TxStatus } from "@/components/TxButton";
import { commitmentTreeAbi, zkGateAbi } from "@/lib/abi";
import { assessAnonymitySet } from "@/lib/anonymity";
import { api } from "@/lib/api";
import { useAnonymitySet, useZkActions, useZkGate } from "@/lib/queries";
import { useTx } from "@/lib/tx";
import { assembleWitness, fetchAllLeaves, generateProof } from "@/lib/zk/assemble";

type Step =
  | { kind: "idle" }
  | { kind: "working"; label: string }
  | { kind: "refused"; reason: string };

// Proves membership in the browser and submits the proof from the connected account.
//
// The secret is held in component state only while the form is filled, is never persisted, and is
// cleared the moment proving begins. It never reaches a request: the tree is fetched by page, the
// commitment is located locally, and the proof — which reveals nothing about which deposit it came
// from — is the only thing that leaves. See docs/v1.3-write-actions-plan.md §2.1.
export function ProveAction() {
  const { address, isConnected } = useAccount();
  const chainId = useChainId();
  const client = usePublicClient();

  const gate = useZkGate();
  const actions = useZkActions();
  const set = useAnonymitySet();
  const tx = useTx();

  const [secret, setSecret] = useState("");
  const [actionId, setActionId] = useState("");
  const [step, setStep] = useState<Step>({ kind: "idle" });

  // Cleared on unmount too, so navigating away mid-form does not leave it in memory longer than
  // the form needs it.
  useEffect(() => () => setSecret(""), []);

  const registered = (actions.data?.items ?? []).filter((a) => a.registered);
  const verdict = set.data ? assessAnonymitySet(set.data.leafCount) : undefined;
  const busy = step.kind === "working" || tx.busy;

  async function prove() {
    const gateAddress = gate.data?.address as `0x${string}` | undefined;
    const treeAddress = gate.data?.tree as `0x${string}` | undefined;
    if (!address || !client || !gateAddress || !treeAddress || !actionId || !secret) return;

    const heldSecret = secret;
    setSecret("");

    try {
      setStep({ kind: "working", label: "Reading the tree from the chain and the indexer…" });
      const leafCount = await client.readContract({
        address: treeAddress,
        abi: commitmentTreeAbi,
        functionName: "leafCount",
      });
      const leaves = await fetchAllLeaves(
        async (limit, offset) => (await api.commitmentsPage(limit, offset)).items,
        leafCount,
      );

      setStep({ kind: "working", label: "Locating your deposit, locally…" });
      const assembled = await assembleWitness({
        secret: heldSecret,
        actionId: actionId as `0x${string}`,
        chainId,
        gate: gateAddress,
        submitter: address,
        leaves,
      });

      // Both checked before proving, so a proof that could never land is not spent twenty seconds
      // generating.
      const known = await client.readContract({
        address: treeAddress,
        abi: commitmentTreeAbi,
        functionName: "isKnownRoot",
        args: [assembled.root],
      });
      if (!known) {
        setStep({
          kind: "refused",
          reason:
            "The tree rebuilt from the indexer does not match any root the chain recognises. The indexer may be behind; try again shortly.",
        });
        return;
      }

      const spent = await client.readContract({
        address: gateAddress,
        abi: zkGateAbi,
        functionName: "isSpent",
        args: [assembled.nullifier],
      });
      if (spent) {
        setStep({ kind: "refused", reason: "This deposit has already been used for this action." });
        return;
      }

      setStep({
        kind: "working",
        label: "Generating the proof in your browser. This takes around twenty seconds…",
      });
      const { proof } = await generateProof(assembled.inputs);

      setStep({ kind: "working", label: "Submitting the proof…" });
      tx.writeContract({
        address: gateAddress,
        abi: zkGateAbi,
        functionName: "executePrivateAction",
        args: [bytesToHex(proof), assembled.root, assembled.nullifier, actionId as `0x${string}`],
      });
      setStep({ kind: "idle" });
    } catch (error) {
      setStep({ kind: "refused", reason: (error as Error).message || "Proving failed." });
    }
  }

  return (
    <Panel title="Use a deposit privately">
      {verdict ? (
        <p className="mb-4 text-sm text-zinc-400" role="note" data-testid="prove-anonymity">
          <strong className="text-zinc-300">{verdict.headline}.</strong> {verdict.detail}
        </p>
      ) : null}

      {!isConnected ? (
        <p className="text-sm text-zinc-400">
          Connect a wallet. The proof is bound to the account that submits it.
        </p>
      ) : (
        <>
          <label className="mb-1 block text-sm text-zinc-500" htmlFor="prove-action">
            Action
          </label>
          <select
            id="prove-action"
            value={actionId}
            onChange={(e) => setActionId(e.target.value)}
            className="mb-3 w-full rounded border border-edge bg-black/40 px-3 py-2 text-sm text-zinc-200"
          >
            <option value="">Choose an action</option>
            {registered.map((a) => (
              <option key={a.actionId} value={a.actionId}>
                {a.name}
              </option>
            ))}
          </select>

          <label className="mb-1 block text-sm text-zinc-500" htmlFor="prove-secret">
            Deposit secret
          </label>
          <input
            id="prove-secret"
            type="password"
            autoComplete="off"
            spellCheck={false}
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
            className="mb-2 w-full rounded border border-edge bg-black/40 px-3 py-2 font-mono text-xs text-zinc-200"
          />
          <p className="mb-3 text-xs text-zinc-500">
            Used only inside this page. It is not stored and is never sent anywhere.
          </p>

          <TxButton disabled={!actionId || !secret || busy} pending={busy} onClick={prove}>
            Prove and submit
          </TxButton>
        </>
      )}

      {step.kind === "working" ? (
        <p className="mt-3 text-xs text-zinc-400" data-testid="prove-step">
          {step.label}
        </p>
      ) : null}
      {step.kind === "refused" ? (
        <p className="mt-3 text-xs text-red-400" role="alert" data-testid="prove-refused">
          {step.reason}
        </p>
      ) : null}

      <TxStatus phase={tx.phase} />
    </Panel>
  );
}
