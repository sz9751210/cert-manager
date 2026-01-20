import { defineConfig } from 'vite'

import react from '@vitejs/plugin-react'

// https://vitejs.dev/config/

export default defineConfig({
    plugins: [react()],
    server: {
        proxy: {
            // 讓開發環境也能把 /api/v1 轉發到後端
            '/api': {
                target: 'http://192.168.25.100:8080',
                changeOrigin: true,
            }
        }
    }
})