import { createApp } from 'vue'
import { Badge, Button, ConfigProvider, DatePicker, Input, Modal, Radio, Select, Skeleton, TimePicker } from 'ant-design-vue'
import dayjs from 'dayjs'
import 'dayjs/locale/zh-cn'
import App from './App.vue'
import 'ant-design-vue/dist/reset.css'
import './styles.css'

dayjs.locale('zh-cn')

const app = createApp(App)

for (const component of [Badge, Button, ConfigProvider, DatePicker, Input, Modal, Radio, Select, Skeleton, TimePicker]) {
  app.use(component)
}

app.mount('#app')
