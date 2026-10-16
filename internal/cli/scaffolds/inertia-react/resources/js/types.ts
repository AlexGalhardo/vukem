// Shapes of the props the Go controllers send.

export interface Post {
  id: number;
  title: string;
  body: string;
  created_at: string;
  updated_at: string;
}

export interface SharedProps {
  appName: string;
  errors: Record<string, string>;
  [key: string]: unknown;
}
