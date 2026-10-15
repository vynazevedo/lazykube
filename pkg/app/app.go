package app

import (
	"context"
	"fmt"
	"github.com/TNK-Studio/lazykube/pkg/config"
	guilib "github.com/TNK-Studio/lazykube/pkg/gui"
	"github.com/TNK-Studio/lazykube/pkg/log"
	"github.com/TNK-Studio/lazykube/pkg/release"
	"github.com/TNK-Studio/lazykube/pkg/utils"
	"github.com/gookit/color"
)

const (
	githubRepo = "https://github.com/TNK-Studio/lazykube"
)

var (
	Version = "No Version Provided"
)

type App struct {
	version     string
	ClusterInfo *guilib.View
	Namespace   *guilib.View
	Service     *guilib.View
	Deployment  *guilib.View
	Pod         *guilib.View
	Navigation  *guilib.View
	Detail      *guilib.View
	Option      *guilib.View
	Gui         *guilib.Gui
}

func NewApp() *App {
	app := &App{
		version:     Version,
		ClusterInfo: ClusterInfo,
		Namespace:   Namespace,
		Service:     Service,
		Deployment:  Deployment,
		Pod:         Pod,
		Navigation:  Navigation,
		Detail:      Detail,
		Option:      Option,
	}

	app.Gui = guilib.NewGui(
		*config.Conf.GuiConfig,
		app.ClusterInfo,
		app.Namespace,
		app.Service,
		app.Deployment,
		app.Pod,
		app.Navigation,
		app.Detail,
		app.Option,
	)
	app.Gui.OnRender = app.OnRender
	app.Gui.OnRenderOptions = app.OnRenderOptions
	app.Gui.Actions = appActions
	app.Gui.OnSizeChange = func(gui *guilib.Gui) error {
		if err := resizePanelHeight(gui); err != nil {
			return err
		}

		return nil
	}
	return app
}

func (app *App) Version() string {
	return app.version
}

func (app *App) CheckRelease() (bool, string, error) {
	result, err := release.NewChecker().Check(context.Background(), app.version, githubRepo)
	if err != nil {
		log.Logger.Error(err)
		return false, "", err
	}
	return result.Outdated, result.Latest, nil
}

func (app *App) Run() {
	app.Gui.Run()
}

func (app *App) Stop() {
	app.Gui.Close()
	isOutdated, version, err := app.CheckRelease()
	if err == nil && isOutdated {
		fmt.Printf(
			"%s. %s => %s %s/releases/tag/%s\n",
			color.Green.Sprint("A new release of lazykube is available"),
			color.Yellow.Sprint(app.Version()),
			color.Green.Sprint(version),
			githubRepo,
			version,
		)
	}
}

func (app *App) OnRender(gui *guilib.Gui) error {
	if config.Conf.UserConfig.CustomResourcePanels != nil {
		for _, resource := range config.Conf.UserConfig.CustomResourcePanels {
			if err := addCustomResourcePanel(gui, resource); err != nil {
				log.Logger.Warningf("app.OnRender - addCustomResourcePanel(gui, %s) error %s", resource, err)
			}
		}
	}
	return nil
}

func (app *App) OnRenderOptions(gui *guilib.Gui) error {
	return gui.RenderString(
		app.Option.Name,
		utils.OptionsMapToString(
			map[string]string{
				"←→↑↓":      "navigate",
				"Ctrl+c":    "exit",
				"Esc":       "back",
				"PgUp/PgDn": "scroll",
				"Home/End":  "top/bottom",
				"Tab":       "next panel",
				"f":         "filter",
				"m":         "more action",
			}),
	)
}
