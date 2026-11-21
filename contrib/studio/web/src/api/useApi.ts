import { useCallback, useEffect, useState } from "react";
import { ApiError, apiGet } from "./client";

export interface ApiState<T> {
  data: T | null;
  error: ApiError | null;
  loading: boolean;
  reload: () => void;
}

interface Settled<T> {
  data: T | null;
  error: ApiError | null;
  loading: boolean;
}

function asApiError(err: unknown): ApiError {
  return err instanceof ApiError ? err : new ApiError(0, "network", "Studio could not be reached.");
}

/**
 * useApi loads one API path and cancels the request when the path changes or
 * the component unmounts. A null path means "nothing to load yet".
 */
export function useApi<T>(path: string | null): ApiState<T> {
  const [state, setState] = useState<Settled<T>>({ data: null, error: null, loading: path !== null });
  const [nonce, setNonce] = useState(0);

  useEffect(() => {
    if (path === null) {
      setState({ data: null, error: null, loading: false });
      return;
    }

    const controller = new AbortController();
    setState({ data: null, error: null, loading: true });

    apiGet<T>(path, controller.signal).then(
      (data) => setState({ data, error: null, loading: false }),
      (err: unknown) => {
        if (!controller.signal.aborted) {
          setState({ data: null, error: asApiError(err), loading: false });
        }
      },
    );

    return () => controller.abort();
  }, [path, nonce]);

  const reload = useCallback(() => setNonce((n) => n + 1), []);

  return { ...state, reload };
}
