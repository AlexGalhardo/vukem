import { Head } from "@inertiajs/react";

import ProjectForm from "../../components/ProjectForm";

export default function Create() {
  return (
    <>
      <Head title="New project" />
      <h1 className="mb-4 text-2xl font-semibold">New project</h1>
      <ProjectForm />
    </>
  );
}
