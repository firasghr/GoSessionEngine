import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "GoSessionEngine — Command Center",
  description: "Real-time NOC dashboard for GoSessionEngine distributed cluster",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en" className="dark">
      <body className="antialiased bg-slate-950 text-slate-100 min-h-screen">
        {children}
      </body>
    </html>
  );
}
