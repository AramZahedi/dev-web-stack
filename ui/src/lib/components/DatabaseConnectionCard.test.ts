import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import DatabaseConnectionCard from './DatabaseConnectionCard.svelte';
import type { DatabaseConnection } from '../databaseTypes';

const MYSQL_CONNECTION: DatabaseConnection = {
  engine: 'mysql',
  host: '127.0.0.1',
  port: 3306,
  database: 'app',
  username: 'app_user',
  password_managed: true,
  url: 'mysql://app_user@127.0.0.1:3306/app',
  env: { DB_DATABASE: 'app', DB_HOST: '127.0.0.1', DB_PORT: '3306', DB_USERNAME: 'app_user' },
};

describe('DatabaseConnectionCard', () => {
  it('shows host, port, database, username, and env snippet', () => {
    render(DatabaseConnectionCard, { props: { connection: MYSQL_CONNECTION, showEnv: true } });
    expect(screen.getByText('127.0.0.1:3306')).toBeTruthy();
    expect(screen.getByText('app')).toBeTruthy();
    expect(screen.getByText('app_user')).toBeTruthy();
    expect(screen.getByText(/DB_DATABASE=app/)).toBeTruthy();
  });

  it('never renders a password value', () => {
    render(DatabaseConnectionCard, { props: { connection: MYSQL_CONNECTION } });
    expect(screen.queryByText(/s3cret/)).toBeNull();
    expect(screen.getByText(/password managed/i)).toBeTruthy();
  });
});
