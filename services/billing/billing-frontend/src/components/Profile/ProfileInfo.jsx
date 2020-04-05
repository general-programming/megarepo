import React from 'react';
import Typography from '@material-ui/core/Typography';
import red from '@material-ui/core/colors/red';
import withStyles from '@material-ui/core/styles/withStyles';

import ErrorIcon from '@material-ui/icons/Error';

import { connect } from 'react-redux';

import Grid from '@material-ui/core/Grid';
import Paper from '@material-ui/core/Paper';
import Button from '@material-ui/core/Button';
import Tooltip from '@material-ui/core/Tooltip';

const styles = theme => ({
    bigContainer: {
        width: '100%',
    },
    outlinedButtom: {
        textTransform: 'uppercase',
        margin: theme.spacing.unit,
    },
    paper: {
        padding: `${theme.spacing.unit}px ${theme.spacing.unit * 3}px ${theme.spacing.unit * 3}px`,
        textAlign: 'left',
        color: theme.palette.text.secondary,
    },
    topInfo: {
        display: 'flex',
        justifyContent: 'space-between',
        alignItems: 'center',
    },
    formControl: {
        width: '100%',
    },
    selectEmpty: {
        marginTop: theme.spacing.unit * 2,
    },
    borderColumn: {
        borderBottom: `1px solid ${theme.palette.grey['100']}`,
        paddingBottom: 24,
        marginBottom: 24,
    },
    flexBar: {
        marginTop: 32,
        display: 'flex',
        justifyContent: 'center',
    },
    emailErrorIcon: {
        marginLeft: theme.spacing.unit,
        color: red['500'],
        verticalAlign: 'middle',
    },
    text: {
        textTransform: 'uppercase',
    }
});

function ProfileInfo(props) {
    const { classes, user } = props;
    let unverifiedEmailElement = null;

    if (!user.email_verified) {
        unverifiedEmailElement = (
            <Tooltip title="Not validated" placement="top">
                <ErrorIcon className={classes.emailErrorIcon} />
            </Tooltip>
        );
    }

    return (
        <React.Fragment>
            <Typography variant="h4" gutterBottom component="h2">
                View Profile
            </Typography>
            <div className={classes.bigContainer}>
                <Paper className={classes.paper}>
                    <div className={classes.topInfo}>
                        <Typography variant="subtitle1" style={{fontWeight: 'bold'}} gutterBottom>
                            User Information
                        </Typography>
                        <Button variant="outlined" size="medium" className={classes.outlinedButtom}>
                            Edit
                        </Button>
                    </div>
                    <Grid item container xs={12}>
                        <Grid item xs={6}>
                            <Typography className={classes.text} color='secondary'>
                                Username
                            </Typography>
                            <Typography variant="h5" gutterBottom>
                                {user.username}
                            </Typography>
                        </Grid>
                        <Grid item xs={6}>
                            <Grid container justify="flex-start" alignItems="center">
                                <Typography className={classes.text} color='secondary'>
                                    Email
                                </Typography>
                                {unverifiedEmailElement}
                            </Grid>
                            <Typography variant="h5" gutterBottom>
                                {user.email}
                            </Typography>
                        </Grid>
                    </Grid>
                </Paper>
            </div>
        </React.Fragment>
    );
}

function mapStateToProps(state) {
    const { user } = state.authentication;
    return {
        user,
    };
}

export default connect(mapStateToProps)(withStyles(styles)(ProfileInfo));
