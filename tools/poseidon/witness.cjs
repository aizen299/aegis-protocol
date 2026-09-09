const { buildPoseidon } = require("circomlibjs");
(async () => {
  const p = await buildPoseidon();
  const F = p.F;
  const h = (xs) => F.toObject(p(xs));

  const DEPTH = 20;
  const secret = 424242n, action = 7n, chain = 31337n, gate = 0xabn;

  const commitment = h([secret]);
  const elements = [], indices = [];
  let cur = commitment;
  for (let i = 0; i < DEPTH; i++) {
    const sib = BigInt(1000 + i);
    const idx = BigInt(i % 2);
    elements.push(sib); indices.push(idx);
    cur = idx === 0n ? h([cur, sib]) : h([sib, cur]);
  }
  const nullifier = h([h([secret, action]), h([chain, gate])]);

  const q = (x) => `"${x.toString()}"`;
  console.log(`root = ${q(cur)}`);
  console.log(`nullifier_hash = ${q(nullifier)}`);
  console.log(`action_id = ${q(action)}`);
  console.log(`chain_id = ${q(chain)}`);
  console.log(`gate = ${q(gate)}`);
  console.log(`secret = ${q(secret)}`);
  console.log(`path_elements = [${elements.map(q).join(", ")}]`);
  console.log(`path_indices = [${indices.map(q).join(", ")}]`);
})();
