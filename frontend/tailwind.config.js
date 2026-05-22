/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{vue,ts}'],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        primary: {
          50: '#faf7f0',
          100: '#f5f0e6',
          200: '#ede8dc',
          300: '#e5dfd2',
          400: '#c9c0ae',
          500: '#9e9080',
          600: '#8a7d6d',
          700: '#5a5040',
          800: '#5a4a30',
          900: '#3a3328',
          950: '#1a1814'
        },
        dark: {
          700: '#d8d0c0',
          800: '#ede8dc',
          900: '#f5f0e6',
          950: '#faf7f0'
        }
      },
      boxShadow: {
        glass: '0 20px 64px rgba(58, 51, 40, 0.08), inset 0 1px 0 rgba(255, 255, 255, 0.56)',
        glow: '0 12px 32px rgba(58, 51, 40, 0.10)'
      },
      backgroundImage: {
        'mesh-gradient': 'radial-gradient(at 50% 16%, rgba(90, 74, 48, 0.10) 0px, transparent 48%), radial-gradient(at 80% 0%, rgba(216, 208, 192, 0.42) 0px, transparent 44%), radial-gradient(at 8% 64%, rgba(74, 138, 72, 0.05) 0px, transparent 46%)'
      }
    }
  },
  plugins: []
}
