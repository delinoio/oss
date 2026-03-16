import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  async rewrites() {
    return [
      {
        source: "/api/thenv/:path*",
        destination: "http://127.0.0.1:8087/:path*",
      },
      {
        source: "/api/commit-tracker/:path*",
        destination: "http://127.0.0.1:8088/:path*",
      },
      {
        source: "/api/remote-file-picker/:path*",
        destination: "http://127.0.0.1:8089/:path*",
      },
    ];
  },
};

export default nextConfig;
