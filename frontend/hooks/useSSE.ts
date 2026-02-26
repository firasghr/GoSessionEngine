"use client";

import { useEffect, useRef } from "react";

/**
 * useSSE subscribes to a Server-Sent Events endpoint and calls `onMessage`
 * for every `data:` event received.  The connection is automatically
 * retried with exponential back-off when the browser closes it.
 *
 * @param url      - Full URL of the SSE endpoint (e.g. "http://localhost:8080/api/metrics/stream")
 * @param onMessage - Callback called with the raw `event.data` string
 * @param enabled  - Set to false to temporarily pause the subscription
 */
export function useSSE(
  url: string,
  onMessage: (data: string) => void,
  enabled = true
) {
  const onMessageRef = useRef(onMessage);
  onMessageRef.current = onMessage;

  useEffect(() => {
    if (!enabled) return;

    let es: EventSource | null = null;
    let retryTimeout: ReturnType<typeof setTimeout>;
    let retryDelay = 500;

    const connect = () => {
      es = new EventSource(url);

      es.onmessage = (event) => {
        retryDelay = 500; // reset back-off on successful message
        onMessageRef.current(event.data);
      };

      es.onerror = () => {
        es?.close();
        es = null;
        retryTimeout = setTimeout(() => {
          retryDelay = Math.min(retryDelay * 2, 10_000);
          connect();
        }, retryDelay);
      };
    };

    connect();

    return () => {
      clearTimeout(retryTimeout);
      es?.close();
    };
  }, [url, enabled]);
}
