// Shapes of the props the Go controllers send.

export interface Project {
  id: number;
  name: string;
  description: string;
  created_at: string;
  updated_at: string;
}

export interface User {
  id: number;
  name: string;
  email: string;
  email_verified_at: string | null;
}

export interface SharedProps {
  appName: string;
  errors: Record<string, string>;
  // Present after vukem install:auth.
  auth?: { user: User | null };
  [key: string]: unknown;
}
