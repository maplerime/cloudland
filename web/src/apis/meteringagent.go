package apis

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"web/src/routes"

	"github.com/gin-gonic/gin"
	"github.com/golang/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/prometheus/prometheus/prompb"
	"github.com/spf13/viper"
)

// meterAPI 结构体
type MeterAPI struct{}

var meterAPI = &MeterAPI{}

// POST: 处理 /meter 路由，补全 instance_id 并转发
func (api *MeterAPI) convert(c *gin.Context) {
	// 1. 读取 body
	fmt.Printf("convert step 0")
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "read body failed"})
		return
	}
	fmt.Printf("convert  step 1 \n")
	// 2. 解压 snappy
	decompressed, err := snappy.Decode(nil, body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "snappy decode failed"})
		return
	}
	fmt.Printf("convert  step 2 decompressed\n")
	// 3. 解析 protobuf
	var req prompb.WriteRequest
	if err := proto.Unmarshal(decompressed, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "protobuf unmarshal failed"})
		return
	}
	fmt.Printf("convert  step 3 req: %+v", req)
	// 4. 遍历 timeseries，补全 instance_id
	for i := range req.Timeseries {
		var domain string
		for _, label := range req.Timeseries[i].Labels {
			if label.Name == "domain" && strings.HasPrefix(label.Value, "inst-") {
				domain = label.Value
				break
			}
		}
		fmt.Printf("convert  step 3.1 domain: %s", domain)
		if domain != "" {
			uuid, err := routes.GetInstanceUUIDByDomain(context.Background(), domain)
			fmt.Printf("convert  step 4 uuid: %+v", uuid)
			if err == nil && uuid != "" {
				// 检查是否已存在 instance_id，避免重复
				found := false
				for _, l := range req.Timeseries[i].Labels {
					if l.Name == "instance_id" {
						found = true
						break
					}
				}
				if !found {
					req.Timeseries[i].Labels = append(req.Timeseries[i].Labels, prompb.Label{
						Name:  "instance_id",
						Value: uuid,
					})
				}
			}
		} else {
			fmt.Printf("convert  step 3.2  no domain")
		}
	}
	fmt.Printf("convert  step 5  again req: %+v", req)
	// 5. 重新编码 protobuf
	newData, err := proto.Marshal(&req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "protobuf marshal failed"})
		return
	}
	fmt.Printf("convert  step 6 newData\n ")
	// 6. 压缩为 snappy
	compressed := snappy.Encode(nil, newData)
	fmt.Printf("convert  step 7 compressed\n")
	// 7. 获取转发目标地址
	forwardURL := viper.GetString("vmagent.forward_url")
	if forwardURL == "" {
		forwardURL = "http://localhost:8428/api/v1/write"
	}
	fmt.Printf("convert  step 8 forwardURL: %+v", forwardURL)
	// 8. 转发到目标
	resp, err := http.Post(forwardURL, "application/x-protobuf", bytes.NewReader(compressed))
	fmt.Printf("convert  step 9 resp: %+v", resp)
	fmt.Printf("convert  step 9 resp: %+v", err)
	if err != nil {
		logger.Error("forward to victoria failed: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "forward failed"})
		return
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	fmt.Printf("convert  step 11 resp: %+v", err)
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}
