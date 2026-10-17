package controllers

import (
	"net/http"

	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/examples/saas/app/models"
	"github.com/AlexGalhardo/vukem/examples/saas/app/policies"
	"github.com/AlexGalhardo/vukem/examples/saas/app/requests"
	"github.com/AlexGalhardo/vukem/inertia"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/web"
)

// ProjectController is the signed-in user's projects, rendered with Inertia
// pages (resources/js/pages/Projects). ProjectPolicy decides every action.
type ProjectController struct {
	db *database.DB
}

// NewProjectController returns a ProjectController using db.
func NewProjectController(db *database.DB) *ProjectController { return &ProjectController{db: db} }

// Index lists the user's projects, filtered by ?q=.
func (pc *ProjectController) Index(c *web.Ctx) error {
	u, err := auth.User[models.User](c, "web")
	if err != nil {
		return err
	}

	q := orm.QueryOf[models.Project](pc.db).Where("user_id", u.ID).OrderBy("id", orm.Desc)
	if search := c.Query("q"); search != "" {
		q = q.Where("name", "LIKE", "%"+search+"%")
	}

	projects, err := q.Get(c.Context())
	if err != nil {
		return err
	}

	return inertia.Render(c, "Projects/Index", inertia.Props{
		"projects":  projects,
		"filters":   map[string]string{"q": c.Query("q")},
		"freeLimit": policies.FreeProjects,
	})
}

// Create shows the form for a new project.
func (*ProjectController) Create(c *web.Ctx) error {
	if err := c.Authorize("create", (*models.Project)(nil)); err != nil {
		return denyCreate(c, err)
	}

	return inertia.Render(c, "Projects/Create", nil)
}

// Store validates and saves a new project.
func (pc *ProjectController) Store(c *web.Ctx) error {
	if err := c.Authorize("create", (*models.Project)(nil)); err != nil {
		return denyCreate(c, err)
	}

	u, err := auth.User[models.User](c, "web")
	if err != nil {
		return err
	}

	in, err := web.Parse(c, requests.ProjectSchema)
	if err != nil {
		return err
	}

	project := models.Project{UserID: u.ID, Name: in.Name, Description: in.Description}
	if err := orm.Create(c.Context(), pc.db, &project); err != nil {
		return err
	}

	inertia.Flash(c, "success", "Project created.")

	return c.RedirectRoute("projects.show", "project", project.ID)
}

// Show displays a project.
func (pc *ProjectController) Show(c *web.Ctx) error {
	project, err := pc.find(c, "view")
	if err != nil {
		return err
	}

	return inertia.Render(c, "Projects/Show", inertia.Props{"project": project})
}

// Edit shows the edit form.
func (pc *ProjectController) Edit(c *web.Ctx) error {
	project, err := pc.find(c, "update")
	if err != nil {
		return err
	}

	return inertia.Render(c, "Projects/Edit", inertia.Props{"project": project})
}

// Update validates and saves changes.
func (pc *ProjectController) Update(c *web.Ctx) error {
	project, err := pc.find(c, "update")
	if err != nil {
		return err
	}

	in, err := web.Parse(c, requests.ProjectSchema)
	if err != nil {
		return err
	}

	project.Name, project.Description = in.Name, in.Description
	if err := orm.Update(c.Context(), pc.db, &project); err != nil {
		return err
	}

	inertia.Flash(c, "success", "Project updated.")

	return c.RedirectRoute("projects.show", "project", project.ID)
}

// Destroy deletes a project and goes back to the list.
func (pc *ProjectController) Destroy(c *web.Ctx) error {
	project, err := pc.find(c, "delete")
	if err != nil {
		return err
	}

	if err := orm.Delete(c.Context(), pc.db, &project); err != nil {
		return err
	}

	inertia.Flash(c, "success", "Project deleted.")

	return c.RedirectRoute("projects.index")
}

// find loads the project in the URL and checks ability on it.
func (pc *ProjectController) find(c *web.Ctx, ability string) (models.Project, error) {
	id, err := c.ParamInt("project")
	if err != nil {
		return models.Project{}, err
	}

	project, err := orm.QueryOf[models.Project](pc.db).Find(c.Context(), id)
	if err != nil {
		return models.Project{}, err
	}

	return project, c.Authorize(ability, &project)
}

// denyCreate answers a refused create: free users over the limit go to the
// billing page, JSON clients get the 403 problem.
func denyCreate(c *web.Ctx, err error) error {
	if errs.Status(err) != http.StatusForbidden || c.WantsJSON() {
		return err
	}

	_ = c.Flash("status", "Free accounts have up to 3 projects. Choose a plan to add more.")

	return c.Redirect("/billing")
}
