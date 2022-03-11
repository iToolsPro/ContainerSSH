## 运行
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build .
sudo docker build . -t imfht/containerssh-testauthconfigserver:v2

## 然后
docker push imfht/containerssh-testauthconfigserver:v2 (可能需要有对应权限),然后在蜜罐的docker-compose里更新这个最新的镜像
