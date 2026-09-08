/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  eslint: { ignoreDuringBuilds: false },
  typescript: { ignoreBuildErrors: false },
  webpack: (config, { webpack }) => {
    // Optional imports from wallet SDKs reached transitively through RainbowKit's wagmi connectors.
    // None is resolvable in a web build and none is on a path this app takes: @x402/* is a Coinbase
    // payments flow, async-storage is React Native only, and pino-pretty is a dev log formatter.
    config.plugins.push(
      new webpack.IgnorePlugin({
        resourceRegExp: /^(@x402\/|@react-native-async-storage\/async-storage$|pino-pretty$)/,
      }),
    );
    return config;
  },
};

export default nextConfig;
