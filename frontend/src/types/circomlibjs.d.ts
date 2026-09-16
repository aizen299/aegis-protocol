declare module "circomlibjs" {
  type Poseidon = ((inputs: (bigint | number | string)[]) => Uint8Array) & {
    F: { toObject(value: Uint8Array): bigint };
  };
  export function buildPoseidon(): Promise<Poseidon>;
}
