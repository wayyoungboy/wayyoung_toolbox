import React from 'react';
import { createRoot } from 'react-dom/client';
import LocaleProvider from '@douyinfe/semi-ui/lib/es/locale/localeProvider';
import zh_CN from '@douyinfe/semi-ui/lib/es/locale/source/zh_CN';
import '@douyinfe/semi-ui/lib/es/_base/base.css';
import App from './App';
import './styles.css';

const requestedTheme = new URLSearchParams(window.location.search).get('theme');
const darkPreference = window.matchMedia('(prefers-color-scheme: dark)');
const applyTheme = () => {
  const theme = requestedTheme === 'light' || requestedTheme === 'dark'
    ? requestedTheme
    : (darkPreference.matches ? 'dark' : 'light');
  document.documentElement.dataset.theme = theme;
  document.body.setAttribute('theme-mode', theme);
};
applyTheme();
darkPreference.addEventListener?.('change', applyTheme);

createRoot(document.getElementById('root')).render(
  <React.StrictMode>
    <LocaleProvider locale={zh_CN}>
      <App />
    </LocaleProvider>
  </React.StrictMode>,
);
