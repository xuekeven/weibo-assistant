import { createApp } from 'vue'
import ui from '@nuxt/ui/vue-plugin'
import App from './App.vue'
import 'ant-design-vue/dist/reset.css'
import './styles.css'

createApp(App).use(ui).mount('#app')
