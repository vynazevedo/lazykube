package app

import (
	"errors"
	"fmt"
	"github.com/TNK-Studio/lazykube/pkg/config"
	guilib "github.com/TNK-Studio/lazykube/pkg/gui"
	"github.com/TNK-Studio/lazykube/pkg/kubecli"
	"github.com/TNK-Studio/lazykube/pkg/log"
	"github.com/atotto/clipboard"
	"github.com/jesseduffield/gocui"
	"math"
	"os"
	"strings"
)

const (
	resourceNotFound = "Resource not found."
	noHistory        = "No History."

	defaultCommand = "/bin/sh"
)

func nextFunctionViewHandler(gui *guilib.Gui, _ *guilib.View) error {
	currentView := gui.CurrentView()
	if currentView == nil {
		return nil
	}

	var nextViewName string
	for index, viewName := range functionViews {
		if currentView.Name == viewName {
			nextIndex := index + 1
			if nextIndex >= len(functionViews) {
				nextIndex = 0
			}
			nextViewName = functionViews[nextIndex]
			log.Logger.Debugf("nextFunctionViewHandler - nextViewName: %s", nextViewName)
			break
		}
	}
	if nextViewName == "" {
		return nil
	}
	gui.ReRenderViews(navigationViewName, detailViewName)
	return gui.FocusView(nextViewName, true)
}

func backToPreviousViewHandler(gui *guilib.Gui, _ *guilib.View) error {
	gui.ReRenderViews(navigationViewName, detailViewName)
	if gui.HasPreviousView() {
		return gui.ReturnPreviousView()
	}

	return gui.FocusView(clusterInfoViewName, false)
}

func toNavigationHandler(gui *guilib.Gui, _ *guilib.View) error {
	return gui.FocusView(navigationViewName, true)
}

func navigationArrowRightHandler(gui *guilib.Gui, _ *guilib.View) error {
	gui.ReRenderViews(navigationViewName, detailViewName)
	options := viewNavigationMap[activeView.Name]
	if navigationIndex+1 >= len(options) {
		return nil
	}
	switchNavigation(gui, navigationIndex+1)
	return nil
}

func navigationArrowLeftHandler(gui *guilib.Gui, _ *guilib.View) error {
	gui.ReRenderViews(navigationViewName, detailViewName)
	if navigationIndex-1 < 0 {
		return gui.ReturnPreviousView()
	}
	switchNavigation(gui, navigationIndex-1)
	return nil
}

func nextPageHandler(_ *guilib.Gui, view *guilib.View) error {
	view.Autoscroll = false
	ox, oy := view.Origin()
	_, height := view.Size()
	newOy := int(math.Min(float64(len(view.ViewBufferLines())), float64(oy+height)))
	view.SetOrigin(ox, newOy)
	return nil
}

func previousPageHandler(_ *guilib.Gui, view *guilib.View) error {
	view.Autoscroll = false
	ox, oy := view.Origin()
	_, height := view.Size()
	newOy := int(math.Max(0, float64(oy-height)))
	view.SetOrigin(ox, newOy)
	return nil
}

func scrollUpHandler(_ *guilib.Gui, view *guilib.View) error {
	view.Autoscroll = false
	ox, oy := view.Origin()
	newOy := int(math.Max(0, float64(oy-2)))
	view.SetOrigin(ox, newOy)
	return nil
}

func scrollDownHandler(_ *guilib.Gui, view *guilib.View) error {
	view.Autoscroll = false
	ox, oy := view.Origin()

	reservedLines := 0
	_, sizeY := view.Size()
	reservedLines = sizeY

	totalLines := len(view.ViewBufferLines())
	if oy+reservedLines >= totalLines {
		view.Autoscroll = true
		return nil
	}

	view.SetOrigin(ox, oy+2)
	return nil
}

func scrollTopHandler(_ *guilib.Gui, view *guilib.View) error {
	view.Autoscroll = false
	ox, _ := view.Origin()
	view.SetOrigin(ox, 0)
	return nil
}

func scrollBottomHandler(_ *guilib.Gui, view *guilib.View) error {
	totalLines := len(view.ViewBufferLines())
	if totalLines == 0 {
		return nil
	}
	_, vy := view.Size()
	if totalLines <= vy {
		return nil
	}

	ox, _ := view.Origin()
	view.Autoscroll = true
	view.SetOrigin(ox, totalLines-1)
	return nil
}

func previousLineHandler(gui *guilib.Gui, view *guilib.View) error {
	currentView := gui.CurrentView()
	if currentView == nil {
		return nil
	}

	_, height := view.Size()
	cx, cy := view.Cursor()
	ox, oy := view.Origin()

	if cy-1 <= 0 && oy-1 > 0 {
		view.SetOrigin(ox, int(math.Max(0, float64(oy-height+1))))

		view.SetCursor(cx, height-1)
		return nil
	}

	view.MoveCursorVertically(-1)
	return nil
}

func nextLineHandler(gui *guilib.Gui, view *guilib.View) error {
	currentView := gui.CurrentView()
	if currentView == nil {
		return nil
	}

	_, height := view.Size()
	cx, cy := view.Cursor()
	if cy+1 >= height-1 {
		ox, oy := view.Origin()
		view.SetOrigin(ox, oy+height-1)
		view.SetCursor(cx, 0)
		return nil
	}

	view.MoveCursorVertically(1)
	return nil
}

func copySelectedLineHandler(gui *guilib.Gui, view *guilib.View) error {
	if view.SelectedLine != "" {
		if err := clipboard.WriteAll(view.SelectedLine); err != nil {
			log.Logger.Warningf("copySelectedLineHandler - clipboard.WriteAll error %s", err)
		}
	}
	currentView := gui.CurrentView()
	if currentView != nil && currentView.Name == moreActionsViewName {
		if err := gui.ReturnPreviousView(); err != nil {
			return err
		}
	}
	return nil
}

func viewSelectedLineChangeHandler(gui *guilib.Gui, view *guilib.View, _ string) error {
	gui.ReRenderViews(view.Name, navigationViewName, detailViewName)
	gui.ClearViews(detailViewName)
	clearDetailViewState(gui)
	return nil
}

func getResourceNamespaceAndName(gui *guilib.Gui, resourceView *guilib.View) (namespace string, resourceName string, err error) {
	if resourceView.Name == namespaceViewName {
		return "", formatSelectedNamespace(resourceView.SelectedLine), nil
	}

	namespace = kubecli.Cli.Namespace()
	selected := resourceView.SelectedLine

	if selected == "" {
		return "", "", errNoResourceSelected
	}

	if !notResourceSelected(namespace) {
		resourceName := formatResourceName(selected, 0)
		if notResourceSelected(resourceName) {
			return "", "", errNoResourceSelected
		}
		return kubecli.Cli.Namespace(), resourceName, nil
	}

	namespace = formatResourceName(selected, 0)
	resourceName = formatResourceName(selected, 1)
	if notResourceSelected(resourceName) {
		return kubecli.Cli.Namespace(), "", errNoResourceSelected
	}

	if notResourceSelected(namespace) {
		namespace = kubecli.Cli.Namespace()
	}

	return namespace, resourceName, nil
}

func editResourceHandler(gui *guilib.Gui, view *guilib.View) error {
	var err error
	var resource, namespace, resourceName string
	if view.Name == detailViewName {
		_, resource, namespace, resourceName, err = resourceMoreActionHandlerHelper(gui, activeView)
	} else {
		_, resource, namespace, resourceName, err = resourceMoreActionHandlerHelper(gui, view)
	}
	if errors.Is(err, errResourceNotFound) || errors.Is(err, errNoResourceSelected) {
		return nil
	}

	if err := gui.RunSuspended(func() error {
		cli(namespace).Edit(newStdStream(), resource, resourceName).Run()
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func rolloutRestartHandler(gui *guilib.Gui, view *guilib.View) error {
	view, resource, namespace, resourceName, err := resourceMoreActionHandlerHelper(gui, view)
	if errors.Is(err, errResourceNotFound) || errors.Is(err, errNoResourceSelected) {
		return nil
	}

	cli(namespace).RolloutRestart(viewStreams(view), resource, resourceName).Run()
	view.ReRender()
	return nil
}

func resourceMoreActionHandlerHelper(gui *guilib.Gui, view *guilib.View) (resourceView *guilib.View, resource string, namespace string, resourceName string, err error) {
	resource = getViewResourceName(view.Name)
	if resource == "" {
		return nil, "", "", "", errResourceNotFound
	}
	namespace, resourceName, err = getResourceNamespaceAndName(gui, view)
	if err != nil {
		return nil, "", "", "", err
	}
	return view, resource, namespace, resourceName, nil
}

func newConfirmDialogHandler(title, relatedViewName string, handler guilib.ViewHandler) guilib.ViewHandler {
	return func(gui *guilib.Gui, view *guilib.View) error {
		if err := showConfirmActionDialog(gui, title, relatedViewName, handler); err != nil {
			return err
		}
		return nil
	}
}

func confirmDialogOptionHandler(gui *guilib.Gui, view *guilib.View, relatedViewName, option string, handler guilib.ViewHandler) error {
	if option == cancelDialogOpt {
		if err := gui.DeleteView(view.Name); err != nil {
			return err
		}
		if err := gui.ReturnPreviousView(); err != nil {
			return err
		}
		return nil
	}

	if option == confirmDialogOpt {
		relatedView, err := gui.GetView(relatedViewName)
		if err != nil {
			return err
		}
		if err := handler(gui, relatedView); err != nil {
			return err
		}
		if err := gui.DeleteView(view.Name); err != nil {
			return err
		}
		if err := gui.ReturnPreviousView(); err != nil {
			return err
		}
		return nil
	}
	return nil
}

func addCustomResourcePanelHandler(gui *guilib.Gui, _ *guilib.View) error {
	stream := newStream()
	kubecli.Cli.APIResources(stream).Run()
	apiResourcesStr := streamToString(stream)

	apiResources := strings.Split(apiResourcesStr, "\n")
	if len(apiResources) > 0 {
		apiResources = apiResources[1:]
	}

	if err := showFilterDialog(
		gui,
		"Filter resource by name.",
		func(resource string) error {
			if resource == "" || resource == resourceNotFound {
				return nil
			}

			resource = formatResourceName(resource, 0)

			if err := addCustomResourcePanel(gui, resource); err != nil {
				return err
			}
			if err := closeFilterDialog(gui); err != nil {
				if errors.Is(err, gocui.ErrUnknownView) {
					return nil
				}
				return err
			}
			return nil
		},
		func(string) ([]string, error) {
			if len(apiResources) >= 1 {
				return apiResources[1:], nil
			}
			return []string{}, nil
		},
		resourceNotFound,
		false,
	); err != nil {
		return err
	}
	return nil
}

func deleteCustomResourcePanelHandler(gui *guilib.Gui, view *guilib.View) error {
	if err := deleteCustomResourcePanel(gui, view.Name); err != nil {
		return err
	}
	return nil
}

func containerExecCommandHandler(gui *guilib.Gui, view *guilib.View) error {
	namespace, resourceName, err := getResourceNamespaceAndName(gui, view)
	if err != nil {
		if errors.Is(err, errNoResourceSelected) {
			return nil
		}
		return err
	}

	containers := getPodContainers(namespace, resourceName)

	if err := showOptionsDialog(
		gui,
		"Please select a container to execute command.",
		1,
		func(containerName string) error {
			if containerName == "" {
				return nil
			}

			if err := showInputDialog(
				gui,
				"Please input command.",
				2,
				func(command string) error {
					if err := runInteractive(gui, func() {
						cli(namespace).
							Exec(newStdStream(), resourceName, command).
							SetFlag("container", containerName).
							SetFlag("tty", "true").
							SetFlag("stdin", "true").
							Run()
					}); err != nil {
						return err
					}
					if err := gui.ReturnPreviousView(); err != nil {
						return err
					}

					gui.ReRenderAll()
					return nil
				},
				defaultCommand,
			); err != nil {
				return err
			}

			return nil
		},
		func() []string {
			return containers
		},
	); err != nil {
		return err
	}

	return nil
}

func changePodLogsContainerHandler(gui *guilib.Gui, view *guilib.View) error {
	podView, err := gui.GetView(podViewName)
	if err != nil {
		return err
	}

	namespace, resourceName, err := getResourceNamespaceAndName(gui, podView)
	if err != nil {
		if errors.Is(err, errNoResourceSelected) {
			return nil
		}
		return err
	}

	containers := getPodContainers(namespace, resourceName)

	if err := showOptionsDialog(
		gui,
		"Please select a container to view logs.",
		1,
		func(containerName string) error {
			if containerName == "" {
				return nil
			}
			if err := view.SetState(logContainerStateKey, containerName, true); err != nil {
				return err
			}
			if err := view.SetState(viewLastRenderTimeStateKey, nil, true); err != nil {
				return err
			}
			if err := view.SetState(logSinceTimeStateKey, nil, true); err != nil {
				return err
			}
			view.Clear()
			view.SetOrigin(0, 0)
			view.ReRender()
			if err := gui.FocusView(detailViewName, false); err != nil {
				return err
			}
			return nil
		},
		func() []string {
			return containers
		},
	); err != nil {
		return err
	}

	return nil
}

func tailLogsHandler(gui *guilib.Gui, view *guilib.View) error {
	if err := view.SetState(ScrollingLogsStateKey, false, false); err != nil {
		return err
	}
	if err := gui.FocusView(detailViewName, false); err != nil {
		return err
	}
	return nil
}

func scrollLogsHandler(gui *guilib.Gui, view *guilib.View) error {
	if err := view.SetState(ScrollingLogsStateKey, true, false); err != nil {
		return err
	}
	if err := gui.FocusView(detailViewName, false); err != nil {
		return err
	}
	return nil
}

func runPodHandler(gui *guilib.Gui, _ *guilib.View) error {
	if err := showFilterDialog(
		gui,
		"Please select a namespace to run a pod.",
		func(namespace string) error {
			namespace = formatSelectedNamespace(namespace)
			if notResourceSelected(namespace) {
				return nil
			}

			return runPodNameInput(gui, namespace)
		},
		func(string) ([]string, error) {
			namespaceView, err := gui.GetView(namespaceViewName)
			if err != nil {
				log.Logger.Error(err)
				return nil, err
			}

			namespaces := namespaceView.ViewBufferLines()
			if len(namespaces) >= 1 {
				return namespaces[1:], nil
			}

			return []string{}, nil
		},
		"No namespaces.",
		false,
	); err != nil {
		return err
	}
	return nil
}

func runPodNameInput(gui *guilib.Gui, namespace string) error {
	if err := showFilterDialog(gui, "Please input pod name.",
		func(podName string) error {
			return runPodImageOptions(gui, namespace, podName)
		},
		func(inputted string) ([]string, error) {
			if config.Conf.UserConfig.History.PodNameHistory != nil {
				return config.Conf.UserConfig.History.PodNameHistory, nil
			}

			return []string{}, nil
		}, noHistory, true); err != nil {
		return err
	}
	return nil
}

func runPodImageOptions(gui *guilib.Gui, namespace, podName string) error {
	if err := showFilterDialog(
		gui,
		"Please input image of container.",
		func(image string) error {
			return runPodCommandInput(gui, namespace, podName, image)
		},
		func(inputted string) ([]string, error) {
			if config.Conf.UserConfig.History.ImageHistory != nil {
				return config.Conf.UserConfig.History.ImageHistory, nil
			}

			return []string{}, nil
		},
		noHistory,
		true,
	); err != nil {
		return err
	}
	return nil
}

func runPodCommandInput(gui *guilib.Gui, namespace, podName, image string) error {
	if err := showFilterDialog(
		gui,
		"Please input command.",
		func(command string) error {
			if err := runInteractive(gui, func() {
				cli(namespace).
					Run(newStdStream(), podName, command).
					SetFlag("rm", "true").
					SetFlag("restart", "Never").
					SetFlag("image-pull-policy", "IfNotPresent").
					SetFlag("tty", "true").
					SetFlag("stdin", "true").
					SetFlag("image", image).
					Run()
			}); err != nil {
				return err
			}
			gui.Config.Cursor = false
			gui.Configure()
			config.Conf.UserConfig.History.AddPodNameHistory(podName)
			config.Conf.UserConfig.History.AddImageHistory(image)
			config.Conf.UserConfig.History.AddCommandHistory(command)
			config.Save()
			if err := gui.ReturnPreviousView(); err != nil {
				return err
			}
			return nil
		},
		func(inputted string) ([]string, error) {
			if config.Conf.UserConfig.History.CommandHistory != nil {
				return config.Conf.UserConfig.History.CommandHistory, nil
			}

			return []string{}, nil
		},
		noHistory,
		true,
	); err != nil {
		return err
	}
	return nil
}

func changeContextHandler(gui *guilib.Gui, view *guilib.View) error {
	if err := showFilterDialog(
		gui,
		"Selected a context to swicth.",
		func(confirmed string) error {
			kubecli.Cli.SetCurrentContext(confirmed)
			gui.ReRenderAll()
			if err := gui.FocusView(clusterInfoViewName, false); err != nil {
				return err
			}
			return nil
		},
		func(inputted string) ([]string, error) {
			return kubecli.Cli.ListContexts(), nil
		},
		"No contexts.",
		false,
	); err != nil {
		return err
	}

	return nil
}

const returnToLazykubeMessage = "Press Enter to return to lazykube."

func runInteractive(gui *guilib.Gui, run func()) error {
	return gui.RunSuspended(func() error {
		run()
		if _, err := fmt.Fprintf(os.Stdout, "\n\n%s\n", returnToLazykubeMessage); err != nil {
			log.Logger.Error(err)
		}
		if _, err := fmt.Scanln(); err != nil {
			log.Logger.Error(err)
		}
		return nil
	})
}
