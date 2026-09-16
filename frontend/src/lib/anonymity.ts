// What an anonymity set of N actually means, said plainly.
//
// docs/v0.4-zk-plan.md §4 records this as unresolved: no circuit fixes a small set, so the size is
// surfaced wherever a proof is discussed rather than left for a reader to infer. A UI that shows a
// leaf count without saying what it buys implies a privacy guarantee the protocol does not make.

export type AnonymityVerdict = {
  tone: "none" | "weak" | "moderate";
  headline: string;
  detail: string;
};

export function assessAnonymitySet(leafCount: number): AnonymityVerdict {
  if (leafCount <= 0) {
    return {
      tone: "none",
      headline: "No commitments",
      detail:
        "The tree is empty. There is nothing to prove membership in, and no proof can be made yet.",
    };
  }

  if (leafCount === 1) {
    return {
      tone: "none",
      headline: "No anonymity at all",
      detail:
        "One commitment means a proof identifies it exactly. Anyone watching knows which deposit " +
        "was spent, because there is only one it could be.",
    };
  }

  if (leafCount < 10) {
    return {
      tone: "weak",
      headline: `One of ${leafCount} — very weak`,
      detail:
        `A proof narrows the spender to ${leafCount} commitments. That is small enough that ` +
        "timing, amounts, or a single off-chain hint will usually identify which one.",
    };
  }

  if (leafCount < 100) {
    return {
      tone: "weak",
      headline: `One of ${leafCount} — weak`,
      detail:
        `A proof narrows the spender to ${leafCount} commitments. Correlation over several actions ` +
        "still reduces that considerably.",
    };
  }

  return {
    tone: "moderate",
    headline: `One of ${leafCount}`,
    detail:
      `A proof narrows the spender to ${leafCount} commitments. This is the whole of the privacy ` +
      "on offer: the set is the guarantee, and it does not grow on its own.",
  };
}
