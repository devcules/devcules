import globals from 'globals';
import reactHooks from 'eslint-plugin-react-hooks';
import tseslint from 'typescript-eslint';

export default tseslint.config(
  { ignores: ['dist', 'node_modules', 'coverage', '../internal'] },
  {
    files: ['**/*.{ts,tsx}'],
    extends: [tseslint.configs.recommended, reactHooks.configs.flat['recommended-latest']],
    languageOptions: {
      globals: { ...globals.browser },
    },
    rules: {
      // Plain words on screen, plain code too: no stray debugging.
      'no-console': 'error',
    },
  },
  {
    files: ['*.config.js', '*.config.ts'],
    languageOptions: {
      globals: { ...globals.node },
    },
  },
);
