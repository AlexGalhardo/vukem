export type PageAuthor = {
  name: string;
};

export type PageIndexProps = {
  owner: PageAuthor;
  projects: PageProject[];
  tree: PageNode;
};

export type PageNode = {
  children: (PageNode | null)[];
  label: string;
};

export type PageProject = {
  Owner: PageAuthor;
  Scores: Record<string, number>;
  created_at: string;
  id: number;
  meta: {
    draft: boolean;
  };
  name: string;
  note?: string;
  reviewer?: PageAuthor | null;
  tags: string[];
};

export type HomeProps = Record<string, never>;

export type ProjectShowProps = PageProject;

export type ProjectsIndexProps = PageIndexProps;
