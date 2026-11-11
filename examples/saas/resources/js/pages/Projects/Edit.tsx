import { Head } from "@inertiajs/react";

import ProjectForm from "../../components/ProjectForm";
import type { Page } from "../../page-props";

export default function Edit({ project }: Page<"Projects/Edit">) {
  return (
    <>
      <Head title={`Edit ${project.name}`} />
      <h1 className="mb-4 text-2xl font-semibold">Edit project</h1>
      <ProjectForm project={project} />
    </>
  );
}
