# Server
## HIGH
- [x] 邀请码有效时间1H
- [x] 添加http模式
- [x] 配置文件添加每个用户最大设备和最大zones上限，默认为 10
- [x] 服务端shell管理脚本 
- [x] jwt refresh-token
## MEDIUM
- [ ] 暴露 swagger 页面
- [ ] 集成Let's Encrypt自动续签
## LOW
- [ ] SMTP邮件自助注册，会发送邀请码到邮箱 ,这个功能可以在配置文件开关
- [ ] 改密码API，关联refresh_tokens
- [ ] 服务端WEB管理页
- [ ] 实现 X-FORWARD 读取源IP
# Client
## 
- [x] 退出登录逻辑改进，如果连不上服务器API(尝试3次)则无法退出登录，弹窗提示，避免服务端产生残留。
## MEDIUM
- [x] 客户端检测最后握手，如果当前选择了连接，但是wg超过一定时间没连上就重新连接，wg源端口每次连接都随机化
## LOW
- [x] UI优化
- [ ] 客户端日志页面
# ALL
## HIGH
- [x] 密码复杂性校验，至少8个字符包含大小写字母和数字
## MEDIUM
- [ ] 设备路由宣告到区域
## LOW
- [ ]  使用 dockerfile 和 docker-compose 部署 增加环境变量处理用来代替 CONFIG配置
- [ ] 构建叠加层，使用wstunnel连接wg隧道，可以 fallback 到普通wg隧道，服务端监听所有协议，客户端手动选择混淆，用于穿透GFW. 或者[ lwo ](https://github.com/ClusterM/wg-obfuscator) quic-go/masque-go, 别忘了MSS限制