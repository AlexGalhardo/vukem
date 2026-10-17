import { Head } from "@inertiajs/react";

import ProjectForm from "../../components/ProjectForm";
import type { Project } from "../../types";

export default function Edit({ project }: { project: Project }) {
  return (
    <>
      <Head title={`Edit ${project.name}`} />
      <h1 className="mb-4 text-2xl font-semibold">Edit project</h1>
      <ProjectForm project={project} />
    </>
  );
}
