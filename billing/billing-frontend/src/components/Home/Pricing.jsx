import React from 'react';
import Button from '@material-ui/core/Button';
import Card from '@material-ui/core/Card';
import CardActions from '@material-ui/core/CardActions';
import CardContent from '@material-ui/core/CardContent';
import CardHeader from '@material-ui/core/CardHeader';
import Grid from '@material-ui/core/Grid';
import StarIcon from '@material-ui/icons/StarBorder';
import Typography from '@material-ui/core/Typography';
import { withStyles } from '@material-ui/core/styles';

const styles = theme => ({
    cardHeader: {
        backgroundColor: theme.palette.grey[200],
    },
    cardPricing: {
        display: 'flex',
        justifyContent: 'center',
        alignItems: 'baseline',
        marginBottom: theme.spacing.unit * 2,
    },
    cardActions: {
        [theme.breakpoints.up('sm')]: {
            paddingBottom: theme.spacing.unit * 2,
        },
    },
    container: {
        marginBottom: theme.spacing.unit * 2,
    }
});

const tiers = [
    {
        title: 'G-Suite',
        price: '10',
        description: [
            'amazing GP email',
            'almost infinite drive for the yiff',
            'premium support provided by nepeat',
        ],
        buttonText: 'Get started',
        buttonVariant: 'outlined',
    },
    {
        title: 'Infrastructure Fee',
        price: '10',
        description: [
            'p2hv',
            '99.9% uptime *',
            'wireguard free! **',
            'you will never want to touch networking again'
        ],
        buttonText: 'Get started',
        buttonVariant: 'contained',
    },
    {
        title: 'Enterprise',
        price: '∞',
        description: [
            'bad memes shipped directly to your door',
            '24/7 yiff support',
            'access to the gp',
        ],
        buttonText: 'Open Pandora\'s box',
        buttonVariant: 'outlined',
    },
];


function Pricing(props) {
    // eslint-disable-next-line react/prop-types
    const { classes } = props;

    return (
        <Grid container spacing={40} alignItems='flex-end' className={classes.container}>
            {tiers.map(tier => (
                // Enterprise card is full width at sm breakpoint
                <Grid item key={tier.title} xs={12} sm={tier.title === 'Enterprise' ? 12 : 6} md={4}>
                    <Card>
                        <CardHeader
                            title={tier.title}
                            subheader={tier.subheader}
                            titleTypographyProps={{ align: 'center' }}
                            subheaderTypographyProps={{ align: 'center' }}
                            action={tier.title === 'Pro' ? <StarIcon /> : null}
                            className={classes.cardHeader}
                        />
                        <CardContent>
                            <div className={classes.cardPricing}>
                                <Typography component='h2' variant='h3' color='textPrimary'>
                                    ${tier.price}
                                </Typography>
                                <Typography variant='h6' color='textSecondary'>
                                    /mo
                                </Typography>
                            </div>
                            {tier.description.map(line => (
                                <Typography variant='subtitle1' align='center' key={line}>
                                    {line}
                                </Typography>
                            ))}
                        </CardContent>
                        <CardActions className={classes.cardActions}>
                            <Button fullWidth variant={tier.buttonVariant} color='primary'>
                                {tier.buttonText}
                            </Button>
                        </CardActions>
                    </Card>
                </Grid>
            ))}
        </Grid>
    );
}


export default withStyles(styles)(Pricing);