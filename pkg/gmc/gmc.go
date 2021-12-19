package gmc

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/ProtobufBot/Go-Mirai-Client/pkg/config"
	"github.com/ProtobufBot/Go-Mirai-Client/pkg/gmc/handler"
	"github.com/ProtobufBot/Go-Mirai-Client/pkg/static"
	"github.com/ProtobufBot/Go-Mirai-Client/pkg/util"

	"github.com/gin-gonic/gin"
	rotatelogs "github.com/lestrrat-go/file-rotatelogs"
	"github.com/rifflock/lfshook"
	log "github.com/sirupsen/logrus"
	easy "github.com/t-tomalak/logrus-easy-formatter"
)

var (
	sms           = false // 参数优先使用短信验证
	wsUrls        = ""    // websocket url
	port          = 9000  // 端口号
	uin     int64 = 0     // qq
	pass          = ""    //password
	device        = ""    // device file path
	help          = false // help
	auth          = ""
	LogPath       = "logs"
)

func init() {
	flag.BoolVar(&sms, "sms", false, "use sms captcha")
	flag.StringVar(&wsUrls, "ws_url", "", "websocket url")
	flag.IntVar(&port, "port", 9000, "admin http api port, 0 is random")
	flag.Int64Var(&uin, "uin", 0, "bot's qq")
	flag.StringVar(&pass, "pass", "", "bot's password")
	flag.StringVar(&device, "device", "", "device file")
	flag.BoolVar(&help, "help", false, "this help")
	flag.StringVar(&auth, "auth", "", "http basic auth: 'username,password'")
	flag.Parse()

	InitLog()
}

func InitLog() {
	// 输出到命令行
	customFormatter := &log.TextFormatter{
		TimestampFormat: "2006-01-02 15:04:05",
		FullTimestamp:   true,
		ForceColors:     true,
	}
	log.SetFormatter(customFormatter)
	log.SetOutput(os.Stdout)

	// 输出到文件
	rotateLogs, err := rotatelogs.New(path.Join(LogPath, "%Y-%m-%d.log"),
		rotatelogs.WithLinkName(path.Join(LogPath, "latest.log")), // 最新日志软链接
		rotatelogs.WithRotationTime(time.Hour*24),                 // 每天一个新文件
		rotatelogs.WithMaxAge(time.Hour*24*3),                     // 日志保留3天
	)
	if err != nil {
		util.FatalError(err)
		return
	}
	log.AddHook(lfshook.NewHook(
		lfshook.WriterMap{
			log.InfoLevel:  rotateLogs,
			log.WarnLevel:  rotateLogs,
			log.ErrorLevel: rotateLogs,
			log.FatalLevel: rotateLogs,
			log.PanicLevel: rotateLogs,
		},
		&easy.Formatter{
			TimestampFormat: "2006-01-02 15:04:05",
			LogFormat:       "[%time%] [%lvl%]: %msg% \r\n",
		},
	))
}

func Start() {
	if help {
		flag.Usage()
		os.Exit(0)
	}

	config.LoadPlugins()  // 如果文件存在，从文件读取gmc config
	LoadParamConfig()     // 如果参数存在，从参数读取gmc config，并覆盖
	config.WritePlugins() // 内存中的gmc config写到文件
	config.Plugins.Range(func(key string, value *config.Plugin) bool {
		log.Infof("Plugin(%s): %s", value.Name, util.MustMarshal(value))
		return true
	})

	CreateBotIfParamExist() // 如果环境变量存在，使用环境变量创建机器人 UIN PASSWORD
	InitGin()               // 初始化GIN HTTP管理
}

func LoadParamConfig() {
	// sms是true，如果本来是true，不变。如果本来是false，变true
	if sms {
		config.SMS = true
	}

	if wsUrls != "" {
		wsUrlList := strings.Split(wsUrls, ",")
		config.ClearPlugins(config.Plugins)
		for i, wsUrl := range wsUrlList {
			plugin := &config.Plugin{Name: strconv.Itoa(i), Urls: []string{wsUrl}}
			config.Plugins.Store(plugin.Name, plugin)
		}
	}

	if port != 9000 {
		config.Port = strconv.Itoa(port)
	}

	if device != "" {
		config.Device = device
	}

	if auth != "" {
		authSplit := strings.Split(auth, ",")
		if len(authSplit) == 2 {
			config.HttpAuth[authSplit[0]] = authSplit[1]
		} else {
			log.Warnf("auth 参数错误，正确格式: 'username,password'")
		}
	}
}

func CreateBotIfParamExist() {
	if uin != 0 && pass != "" {
		log.Infof("使用参数创建机器人 %d", uin)
		go func() {
			handler.CreateBotImpl(uin, pass, 0, 0)
		}()
	}
}

func InitGin() {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	if len(config.HttpAuth) > 0 {
		router.Use(gin.BasicAuth(config.HttpAuth))
	}

	router.Use(handler.CORSMiddleware())
	router.StaticFS("/", http.FS(static.MustGetStatic()))
	router.POST("/bot/create/v1", handler.CreateBot)
	router.POST("/bot/delete/v1", handler.DeleteBot)
	router.POST("/bot/list/v1", handler.ListBot)
	router.POST("/captcha/solve/v1", handler.SolveCaptcha)
	router.POST("/qrcode/fetch/v1", handler.FetchQrCode)
	router.POST("/qrcode/query/v1", handler.QueryQRCodeStatus)
	router.POST("/plugin/list/v1", handler.ListPlugin)
	router.POST("/plugin/save/v1", handler.SavePlugin)
	router.POST("/plugin/delete/v1", handler.DeletePlugin)
	realPort, err := RunGin(router, ":"+config.Port)
	if err != nil {
		util.FatalError(fmt.Errorf("failed to run gin, err: %+v", err))
	}
	config.Port = realPort
	log.Infof("端口号 %s", realPort)
	log.Infof(fmt.Sprintf("浏览器打开 http://localhost:%s/ 设置机器人", realPort))
}

func RunGin(engine *gin.Engine, port string) (string, error) {
	ln, err := net.Listen("tcp", port)
	if err != nil {
		return "", err
	}
	_, randPort, _ := net.SplitHostPort(ln.Addr().String())
	go func() {
		if err := http.Serve(ln, engine); err != nil {
			util.FatalError(fmt.Errorf("failed to serve http, err: %+v", err))
		}
	}()
	return randPort, nil
}
