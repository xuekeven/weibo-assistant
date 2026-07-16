import { createApp } from 'vue'
import { Badge, Button, ConfigProvider, DatePicker, Input, Radio, Select, Skeleton, TimePicker } from 'ant-design-vue'
import App from './App.vue'
import 'ant-design-vue/dist/reset.css'
import './styles.css'

const app = createApp(App)

for (const component of [Badge, Button, ConfigProvider, DatePicker, Input, Radio, Select, Skeleton, TimePicker]) {
  app.use(component)
}

app.mount('#app')
