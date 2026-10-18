// The example Posts CRUD this starter ships with.
//
// vukem install:inertia generates resources/js/pages/Posts/* together with the
// demo controller, its routes and its tests. The navigation entry and the home
// page call to action are rendered only while those pages are present, so
// deleting resources/js/pages/Posts/ (with components/PostForm.tsx and the Go
// route they belong to) takes the links with it instead of leaving them
// pointing at a route that is gone.
//
// Vite resolves the glob at build time; an empty match is a valid, empty map.
const demoPages = import.meta.glob("./pages/Posts/*.tsx");

// demoInstalled reports whether the demo resource's pages are still present.
export const demoInstalled: boolean = Object.keys(demoPages).length > 0;

// demoLink points at the demo resource's index page.
export const demoLink = { href: "/posts", label: "Posts" } as const;
