# lazykube

通过鼠标和命令行交互的方式来管理 K8s 集群

([English Document](../README.md) | [中文文档](README_CN.md))

![gif](./lazykube.gif)

## 安装方式

需要 Go 1.26 或更高版本。如果本地 Go 版本较旧，在 `GOTOOLCHAIN=auto`（默认值）时会自动下载所需的工具链。

```bash
$ git clone https://github.com/vynazevedo/lazykube.git
$ cd lazykube
$ go build -o lazykube ./cmd/lazykube
$ sudo mv lazykube /usr/local/bin/
```

## 如何使用

```bash
$ lazykube
```

lazykube 使用 kubeconfig（`KUBECONFIG` 或 `~/.kube/config`）中的当前上下文。

## 常见问题

* 当使用 iterm2 作为终端时鼠标无法交互 ?

![iterm2-enable-mouse-reporting](./iterm2-enable-mouse-reporting.png)

## 更新日志

[更新日志](../CHANGELOG.md)
