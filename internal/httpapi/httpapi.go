// Package httpapi adapts the internal services to REST. Handlers resolve
// employer context, read X-Operator on writes, and render the same
// machine-readable error envelope as the CLI.
package httpapi

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	"github.com/PithomLabs/a1/internal/apperr"
	"github.com/PithomLabs/a1/internal/attendance"
	"github.com/PithomLabs/a1/internal/employer"
	"github.com/PithomLabs/a1/internal/hr"
	"github.com/PithomLabs/a1/internal/payroll"
	"github.com/PithomLabs/a1/internal/timesheet"
	"github.com/PithomLabs/a1/internal/wfm"
)

// Register mounts the implemented slice surface.
func Register(e *echo.Echo, pool *pgxpool.Pool) error {
	v1 := e.Group("/api/v1")
	v1.POST("/employers", func(c echo.Context) error {
		h, err := resolve(c, pool, false)
		if err != nil {
			return fail(c, err)
		}
		var in employer.EmployerInput
		if err := c.Bind(&in); err != nil {
			return fail(c, err)
		}
		created, err := employer.Create(h.ctx, pool, in, h.operator)
		if err != nil {
			return fail(c, err)
		}
		if created.Existed {
			return c.JSON(http.StatusOK, created)
		}
		return c.JSON(http.StatusCreated, created)
	})
	v1.GET("/employers/:employer_id", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, h.employer)
	})
	v1.GET("/employers/:employer_id/employees", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		list, err := hr.ListEmployees(h.ctx, pool, h.employer.ID)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, list)
	})
	v1.POST("/employers/:employer_id/employees", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		var in hr.EmployeeInput
		if err := c.Bind(&in); err != nil {
			return fail(c, apperr.New(apperr.CodeValidation, "invalid body: %v", err))
		}
		created, err := hr.CreateEmployee(h.ctx, pool, h.employer.ID, in, h.operator)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusCreated, created)
	})
	v1.GET("/employers/:employer_id/employees/:employee_ref", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		e, err := hr.GetEmployee(h.ctx, pool, h.employer.ID, c.Param("employee_ref"))
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, e)
	})
	v1.PATCH("/employers/:employer_id/employees/:employee_ref", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		var in struct {
			Status    string `json:"status"`
			Reason    string `json:"reason"`
			Effective string `json:"effective"`
		}
		if err := c.Bind(&in); err != nil {
			return fail(c, apperr.New(apperr.CodeValidation, "invalid body: %v", err))
		}
		e, err := hr.GetEmployee(h.ctx, pool, h.employer.ID, c.Param("employee_ref"))
		if err != nil {
			return fail(c, err)
		}
		updated, err := hr.SetStatus(h.ctx, pool, h.employer.ID, e.ID, in.Status, in.Effective, in.Reason, h.operator)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, updated)
	})
	v1.POST("/employers/:employer_id/employees/:employee_ref/terms", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		var in hr.TermsInput
		if err := c.Bind(&in); err != nil {
			return fail(c, apperr.New(apperr.CodeValidation, "invalid body: %v", err))
		}
		e, err := hr.GetEmployee(h.ctx, pool, h.employer.ID, c.Param("employee_ref"))
		if err != nil {
			return fail(c, err)
		}
		t, err := hr.SetTerms(h.ctx, pool, h.employer.ID, e.ID, in, h.operator)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusCreated, t)
	})
	v1.GET("/employers/:employer_id/employees/:employee_ref/terms", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		e, err := hr.GetEmployee(h.ctx, pool, h.employer.ID, c.Param("employee_ref"))
		if err != nil {
			return fail(c, err)
		}
		list, err := hr.ListTerms(h.ctx, pool, h.employer.ID, e.ID)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, list)
	})
	v1.POST("/employers/:employer_id/employees/:employee_ref/terms/:terms_id/compensation", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		var in hr.CompensationInput
		if err := c.Bind(&in); err != nil {
			return fail(c, apperr.New(apperr.CodeValidation, "invalid body: %v", err))
		}
		e, err := hr.GetEmployee(h.ctx, pool, h.employer.ID, c.Param("employee_ref"))
		if err != nil {
			return fail(c, err)
		}
		if err := checkTermsOwner(h.ctx, pool, h.employer.ID, e.ID, c.Param("terms_id")); err != nil {
			return fail(c, err)
		}
		id, err := hr.AddCompensation(h.ctx, pool, h.employer.ID, c.Param("terms_id"), in, h.operator)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusCreated, map[string]string{"id": id})
	})
	v1.GET("/employers/:employer_id/employees/:employee_ref/terms/:terms_id/compensation", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		e, err := hr.GetEmployee(h.ctx, pool, h.employer.ID, c.Param("employee_ref"))
		if err != nil {
			return fail(c, err)
		}
		if err := checkTermsOwner(h.ctx, pool, h.employer.ID, e.ID, c.Param("terms_id")); err != nil {
			return fail(c, err)
		}
		list, err := hr.ListCompensation(h.ctx, pool, h.employer.ID, c.Param("terms_id"))
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, list)
	})
	v1.POST("/employers/:employer_id/employees/:employee_ref/terms/:terms_id/compensation/:line_id/corrections", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		var in hr.CorrectionInput
		if err := c.Bind(&in); err != nil {
			return fail(c, apperr.New(apperr.CodeValidation, "invalid body: %v", err))
		}
		e, err := hr.GetEmployee(h.ctx, pool, h.employer.ID, c.Param("employee_ref"))
		if err != nil {
			return fail(c, err)
		}
		if err := checkTermsOwner(h.ctx, pool, h.employer.ID, e.ID, c.Param("terms_id")); err != nil {
			return fail(c, err)
		}
		if err := checkLineOwner(h.ctx, pool, h.employer.ID, c.Param("terms_id"), c.Param("line_id")); err != nil {
			return fail(c, err)
		}
		id, err := hr.CorrectCompensation(h.ctx, pool, h.employer.ID, c.Param("line_id"), in, h.operator)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusCreated, map[string]string{"id": id})
	})
	v1.POST("/employers/:employer_id/employees/:employee_ref/shg", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		var in hr.SHGInput
		if err := c.Bind(&in); err != nil {
			return fail(c, apperr.New(apperr.CodeValidation, "invalid body: %v", err))
		}
		s, err := hr.SetSHG(h.ctx, pool, h.employer.ID, c.Param("employee_ref"), in, h.operator)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusCreated, s)
	})
	v1.GET("/employers/:employer_id/employees/:employee_ref/shg", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		list, err := hr.ListSHG(h.ctx, pool, h.employer.ID, c.Param("employee_ref"))
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, list)
	})
	v1.POST("/employers/:employer_id/clients", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		var in struct {
			Code string `json:"code"`
			Name string `json:"name"`
		}
		if err := c.Bind(&in); err != nil {
			return fail(c, apperr.New(apperr.CodeValidation, "invalid body: %v", err))
		}
		created, err := wfm.CreateClient(h.ctx, pool, h.employer.ID, in.Code, in.Name)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusCreated, created)
	})
	v1.GET("/employers/:employer_id/clients", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		list, err := wfm.ListClients(h.ctx, pool, h.employer.ID)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, list)
	})
	v1.POST("/employers/:employer_id/programmes", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		var in wfm.ProgrammeInput
		if err := c.Bind(&in); err != nil {
			return fail(c, apperr.New(apperr.CodeValidation, "invalid body: %v", err))
		}
		created, err := wfm.CreateProgramme(h.ctx, pool, h.employer.ID, in)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusCreated, created)
	})
	v1.GET("/employers/:employer_id/programmes", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		list, err := wfm.ListProgrammes(h.ctx, pool, h.employer.ID)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, list)
	})
	v1.POST("/employers/:employer_id/programmes/:programme_id/require-skills", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		var grants []wfm.SkillGrant
		if err := c.Bind(&grants); err != nil {
			return fail(c, apperr.New(apperr.CodeValidation, "invalid body: %v", err))
		}
		if err := wfm.RequireSkills(h.ctx, pool, h.employer.ID, c.Param("programme_id"), grants, h.operator); err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, map[string]string{"programme": c.Param("programme_id"), "result": "ok"})
	})
	v1.POST("/employers/:employer_id/teams", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		var in wfm.TeamInput
		if err := c.Bind(&in); err != nil {
			return fail(c, apperr.New(apperr.CodeValidation, "invalid body: %v", err))
		}
		created, err := wfm.CreateTeam(h.ctx, pool, h.employer.ID, in)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusCreated, created)
	})
	v1.GET("/employers/:employer_id/teams", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		list, err := wfm.ListTeams(h.ctx, pool, h.employer.ID)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, list)
	})
	v1.POST("/employers/:employer_id/teams/:team_id/members", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		var in struct {
			Employee      string `json:"employee"`
			Role          string `json:"role"`
			EffectiveFrom string `json:"effective_from"`
		}
		if err := c.Bind(&in); err != nil {
			return fail(c, apperr.New(apperr.CodeValidation, "invalid body: %v", err))
		}
		id, err := wfm.AddMembership(h.ctx, pool, h.employer.ID, c.Param("team_id"), in.Employee, in.Role, in.EffectiveFrom, h.operator)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusCreated, map[string]string{"id": id})
	})
	v1.POST("/employers/:employer_id/shift-templates", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		var in wfm.TemplateInput
		if err := c.Bind(&in); err != nil {
			return fail(c, apperr.New(apperr.CodeValidation, "invalid body: %v", err))
		}
		created, err := wfm.CreateTemplate(h.ctx, pool, h.employer.ID, in)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusCreated, created)
	})
	v1.GET("/employers/:employer_id/shift-templates", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		list, err := wfm.ListTemplates(h.ctx, pool, h.employer.ID)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, list)
	})
	v1.POST("/employers/:employer_id/skills", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		var in struct {
			Employee  string           `json:"employee"`
			ValidFrom string           `json:"valid_from"`
			Grants    []wfm.SkillGrant `json:"grants"`
		}
		if err := c.Bind(&in); err != nil {
			return fail(c, apperr.New(apperr.CodeValidation, "invalid body: %v", err))
		}
		if err := wfm.AssignSkills(h.ctx, pool, h.employer.ID, in.Employee, in.ValidFrom, in.Grants, h.operator); err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusCreated, map[string]string{"employee": in.Employee})
	})
	v1.POST("/employers/:employer_id/rosters", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		var in wfm.RosterInput
		if err := c.Bind(&in); err != nil {
			return fail(c, apperr.New(apperr.CodeValidation, "invalid body: %v", err))
		}
		created, err := wfm.CreateRoster(h.ctx, pool, h.employer.ID, in)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusCreated, created)
	})
	v1.GET("/employers/:employer_id/rosters", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		list, err := wfm.ListRosters(h.ctx, pool, h.employer.ID)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, list)
	})
	v1.POST("/employers/:employer_id/rotations", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		var in wfm.CreateRotationAssignInput
		if err := c.Bind(&in); err != nil {
			return fail(c, apperr.New(apperr.CodeValidation, "invalid body: %v", err))
		}
		a, err := wfm.CreateRotationAssign(h.ctx, pool, h.employer.ID, in)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusCreated, a)
	})
	v1.GET("/employers/:employer_id/rotations", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		list, err := wfm.ListRotationAssigns(h.ctx, pool, h.employer.ID)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, list)
	})
	v1.GET("/employers/:employer_id/rosters/:roster_id", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		d, err := wfm.Show(h.ctx, pool, h.employer.ID, c.Param("roster_id"))
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, d)
	})
	v1.POST("/employers/:employer_id/rosters/:roster_id/assignments", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		var in wfm.AssignmentInput
		if err := c.Bind(&in); err != nil {
			return fail(c, apperr.New(apperr.CodeValidation, "invalid body: %v", err))
		}
		a, err := wfm.Assign(h.ctx, pool, h.employer.ID, c.Param("roster_id"), in)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusCreated, a)
	})
	v1.POST("/employers/:employer_id/rosters/:roster_id/publish", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		r, err := wfm.Publish(h.ctx, pool, h.employer.ID, c.Param("roster_id"), h.operator)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, r)
	})
	v1.POST("/employers/:employer_id/rosters/:roster_id/revise", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		var in struct {
			Reason string `json:"reason"`
		}
		if err := c.Bind(&in); err != nil {
			return fail(c, apperr.New(apperr.CodeValidation, "invalid body: %v", err))
		}
		r, err := wfm.Revise(h.ctx, pool, h.employer.ID, c.Param("roster_id"), in.Reason, h.operator)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, r)
	})
	v1.POST("/employers/:employer_id/clock-events", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		fh, err := c.FormFile("file")
		if err != nil {
			return fail(c, apperr.New(apperr.CodeValidation, "multipart file is required"))
		}
		f, err := fh.Open()
		if err != nil {
			return fail(c, apperr.New(apperr.CodeValidation, "open upload: %v", err))
		}
		defer f.Close()
		rep, err := attendance.Import(h.ctx, pool, h.employer.ID, f, h.operator, timesheet.RegenerateDraft, timesheet.ChainTerminal)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, rep)
	})
	v1.GET("/employers/:employer_id/attendance", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		from, to := c.QueryParam("from"), c.QueryParam("to")
		if from == "" || to == "" {
			return fail(c, apperr.New(apperr.CodeValidation, "from and to query params are required"))
		}
		rev, err := attendance.ReviewAttendance(h.ctx, pool, h.employer.ID, from, to)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, rev)
	})
	v1.GET("/employers/:employer_id/attendance/intervals", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		from, to := c.QueryParam("from"), c.QueryParam("to")
		if from == "" || to == "" {
			return fail(c, apperr.New(apperr.CodeValidation, "from and to query params are required"))
		}
		list, err := attendance.ListIntervals(h.ctx, pool, h.employer.ID, c.QueryParam("employee"), from, to)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, list)
	})
	v1.POST("/employers/:employer_id/attendance/corrections", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		var in attendance.CorrectionInput
		if err := c.Bind(&in); err != nil {
			return fail(c, apperr.New(apperr.CodeValidation, "invalid body: %v", err))
		}
		rep, err := attendance.Correct(h.ctx, pool, h.employer.ID, in, h.operator, timesheet.RegenerateDraft, timesheet.CorrectionDrafts)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusCreated, rep)
	})
	v1.POST("/employers/:employer_id/attendance/events/:event_id/disposition", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		var in attendance.DispositionInput
		if err := c.Bind(&in); err != nil {
			return fail(c, apperr.New(apperr.CodeValidation, "invalid body: %v", err))
		}
		in.EventID = c.Param("event_id")
		rep, err := attendance.DispositionDuplicate(h.ctx, pool, h.employer.ID, in, h.operator, timesheet.RegenerateDraft, timesheet.ChainTerminal)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusCreated, rep)
	})
	v1.POST("/employers/:employer_id/attendance/attach", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		var in struct {
			EmployeeNo string `json:"employee_no"`
		}
		if err := c.Bind(&in); err != nil {
			return fail(c, apperr.New(apperr.CodeValidation, "invalid body: %v", err))
		}
		rep, err := attendance.AttachPending(h.ctx, pool, h.employer.ID, in.EmployeeNo, h.operator, timesheet.RegenerateDraft, timesheet.ChainTerminal)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, rep)
	})
	v1.POST("/employers/:employer_id/timesheets/generate", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		var in struct {
			PayPeriod string `json:"pay_period"`
		}
		if err := c.Bind(&in); err != nil {
			return fail(c, apperr.New(apperr.CodeValidation, "invalid body: %v", err))
		}
		g, err := timesheet.Generate(h.ctx, pool, h.employer.ID, in.PayPeriod, h.operator)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, g)
	})
	v1.GET("/employers/:employer_id/timesheets", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		list, err := timesheet.ListTimesheets(h.ctx, pool, h.employer.ID, c.QueryParam("period"), c.QueryParam("employee"))
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, list)
	})
	v1.GET("/employers/:employer_id/pay-periods", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		list, err := timesheet.ListPeriods(h.ctx, pool, h.employer.ID)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, list)
	})
	v1.POST("/employers/:employer_id/pay-periods", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		var in timesheet.PeriodInput
		if err := c.Bind(&in); err != nil {
			return fail(c, apperr.New(apperr.CodeValidation, "invalid body: %v", err))
		}
		p, err := timesheet.CreatePeriod(h.ctx, pool, h.employer.ID, in, h.operator)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusCreated, p)
	})
	v1.GET("/employers/:employer_id/timesheets/:timesheet_id", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		t, err := timesheet.Get(h.ctx, pool, h.employer.ID, c.Param("timesheet_id"))
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, t)
	})
	v1.POST("/employers/:employer_id/timesheets/:timesheet_id/submit", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		t, err := timesheet.Submit(h.ctx, pool, h.employer.ID, c.Param("timesheet_id"), h.operator)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, t)
	})
	v1.POST("/employers/:employer_id/timesheets/:timesheet_id/approve", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		t, err := timesheet.Approve(h.ctx, pool, h.employer.ID, c.Param("timesheet_id"), h.operator)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, t)
	})
	v1.POST("/employers/:employer_id/timesheets/:timesheet_id/reject", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		t, err := timesheet.Reject(h.ctx, pool, h.employer.ID, c.Param("timesheet_id"), h.operator)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, t)
	})
	v1.POST("/employers/:employer_id/timesheets/:timesheet_id/reissue", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		t, err := timesheet.Reissue(h.ctx, pool, h.employer.ID, c.Param("timesheet_id"), h.operator)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusCreated, t)
	})
	v1.POST("/employers/:employer_id/payroll-runs", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		var in struct {
			Period string `json:"period"`
			Kind   string `json:"kind"`
		}
		if err := c.Bind(&in); err != nil {
			return fail(c, apperr.New(apperr.CodeValidation, "invalid body: %v", err))
		}
		r, err := payroll.CreateRun(h.ctx, pool, h.employer.ID, in.Period, in.Kind, h.operator)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusCreated, r)
	})
	v1.POST("/employers/:employer_id/payroll-runs/:run_id/exclusions", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		var in struct {
			Employee string `json:"employee"`
			Reason   string `json:"reason"`
		}
		if err := c.Bind(&in); err != nil {
			return fail(c, apperr.New(apperr.CodeValidation, "invalid body: %v", err))
		}
		id, err := payroll.AddExclusion(h.ctx, pool, h.employer.ID, c.Param("run_id"), in.Employee, in.Reason, h.operator)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusCreated, map[string]string{"id": id})
	})
	v1.GET("/employers/:employer_id/payroll-readiness", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		period := c.QueryParam("pay_period")
		if period == "" {
			return fail(c, apperr.New(apperr.CodeValidation, "pay_period query param is required"))
		}
		var runID *string
		if r := c.QueryParam("run"); r != "" {
			runID = &r
		}
		r, err := payroll.Assess(h.ctx, pool, h.employer.ID, period, runID)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, r)
	})
	v1.GET("/employers/:employer_id/rule-activations", func(c echo.Context) error {
		h, err := resolve(c, pool, true)
		if err != nil {
			return fail(c, err)
		}
		rows, err := pool.Query(h.ctx,
			`SELECT pack, version, status FROM rule_pack_activation WHERE employer_id = $1 ORDER BY pack`, h.employer.ID)
		if err != nil {
			return fail(c, apperr.FromDB(err, "list activations"))
		}
		defer rows.Close()
		type activation struct {
			Pack    string `json:"pack"`
			Version string `json:"version"`
			Status  string `json:"status"`
		}
		out := []activation{}
		for rows.Next() {
			var a activation
			if err := rows.Scan(&a.Pack, &a.Version, &a.Status); err != nil {
				return fail(c, apperr.FromDB(err, "scan activation"))
			}
			out = append(out, a)
		}
		if err := rows.Err(); err != nil {
			return fail(c, apperr.FromDB(err, "list activations"))
		}
		return c.JSON(http.StatusOK, out)
	})
	return nil
}

// handler is the resolved per-request context.
type handler struct {
	ctx      context.Context
	pool     *pgxpool.Pool
	employer *employer.Employer
	operator string
}

// resolve builds request context: employer and operator attribution.
// Mutations require an explicit X-Operator header: the server's own
// login is not the caller's identity, so neither it nor a generic
// label may silently stand in. Reads keep a harmless default.
func resolve(c echo.Context, pool *pgxpool.Pool, needEmployer bool) (*handler, error) {
	h := &handler{ctx: c.Request().Context(), pool: pool, operator: c.Request().Header.Get("X-Operator")}
	if h.operator == "" && c.Request().Method != http.MethodGet {
		return nil, apperr.New(apperr.CodeValidation, "X-Operator header is required")
	}
	if h.operator == "" {
		h.operator = "api"
	}
	if needEmployer {
		e, err := employer.Resolve(h.ctx, pool, c.Param("employer_id"))
		if err != nil {
			return nil, err
		}
		h.employer = e
	}
	return h, nil
}

// checkTermsOwner keeps nested compensation routes honest: the terms row
// must belong to the employee named in the path.
func checkTermsOwner(ctx context.Context, pool *pgxpool.Pool, employerID, employeeID, termsID string) error {
	var ok bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM employment_terms WHERE employer_id = $1 AND id = $2 AND employee_id = $3)`,
		employerID, termsID, employeeID).Scan(&ok); err != nil {
		return apperr.FromDB(err, "check terms ownership")
	}
	if !ok {
		return apperr.New(apperr.CodeNotFound, "terms %s not found for this employee", termsID)
	}
	return nil
}

// checkLineOwner keeps the correction route honest: the line must sit
// under the terms row named in the path.
func checkLineOwner(ctx context.Context, pool *pgxpool.Pool, employerID, termsID, lineID string) error {
	var ok bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM compensation_line WHERE employer_id = $1 AND id = $2 AND terms_id = $3)`,
		employerID, lineID, termsID).Scan(&ok); err != nil {
		return apperr.FromDB(err, "check line ownership")
	}
	if !ok {
		return apperr.New(apperr.CodeNotFound, "compensation %s not found under these terms", lineID)
	}
	return nil
}

// fail renders the shared machine-readable error envelope. Every error
// carries the request ID so a client report maps to one server log
// line. Internal failures log their detail server-side; the client
// sees only the safe message.
func fail(c echo.Context, err error) error {
	code := apperr.AsCode(err)
	status := http.StatusInternalServerError
	msg := err.Error()
	if ae, ok := err.(*apperr.Error); ok {
		status = ae.Status
		msg = ae.Message
	} else {
		msg = "request failed"
	}
	rid := c.Response().Header().Get(echo.HeaderXRequestID)
	if rid == "" {
		rid = c.Request().Header.Get(echo.HeaderXRequestID)
	}
	if code == apperr.CodeInternal {
		c.Logger().Errorf("request %s failed: %v", rid, err)
	}
	return c.JSON(status, map[string]string{"error": code, "message": msg, "request_id": rid})
}
